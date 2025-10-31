package blocks

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

type HeadEvent struct {
	HeadData *protocol.BlockData
}

type HeadProcessor struct {
	upstreamId           string
	lifecycle            *utils.BaseLifecycle
	head                 Head
	lastUpdate           *utils.Atomic[time.Time]
	headNoUpdatesTimeout time.Duration
	subManager           *utils.SubscriptionManager[HeadEvent]
	manualHeadChan       chan *protocol.Block
}

var _ utils.Lifecycle = (*HeadProcessor)(nil)

func NewHeadProcessor(
	ctx context.Context,
	upConfig *config.Upstream,
	apiConnector connectors.ApiConnector,
	specific specific.ChainSpecific,
) *HeadProcessor {
	configuredChain := chains.GetChain(upConfig.ChainName)
	head := createHead(ctx, upConfig.Id, upConfig.PollInterval, apiConnector, specific, upConfig.Options)

	headNoUpdatesTimeout := 1 * time.Minute
	switch head.(type) {
	case *RpcHead:
		if upConfig.PollInterval >= headNoUpdatesTimeout {
			headNoUpdatesTimeout = upConfig.PollInterval * 3
		}
	case *SubscriptionHead:
		if configuredChain.Settings.ExpectedBlockTime >= headNoUpdatesTimeout {
			headNoUpdatesTimeout = configuredChain.Settings.ExpectedBlockTime + headNoUpdatesTimeout
		}
	}

	name := fmt.Sprintf("%s_head_processor", upConfig.Id)
	return &HeadProcessor{
		upstreamId:           upConfig.Id,
		head:                 head,
		manualHeadChan:       make(chan *protocol.Block, 100),
		lifecycle:            utils.NewBaseLifecycle(name, ctx),
		headNoUpdatesTimeout: headNoUpdatesTimeout,
		lastUpdate:           utils.NewAtomic[time.Time](),
		subManager:           utils.NewSubscriptionManager[HeadEvent](name),
	}
}

func (h *HeadProcessor) GetCurrentBlock() *protocol.Block {
	return h.head.GetCurrentBlock()
}

func (h *HeadProcessor) Subscribe(name string) *utils.Subscription[HeadEvent] {
	return h.subManager.Subscribe(name)
}

func (h *HeadProcessor) Running() bool {
	return h.lifecycle.Running()
}

func (h *HeadProcessor) Start() {
	h.lifecycle.Start(func(ctx context.Context) error {
		go h.head.Start()
		h.lastUpdate.Store(time.Now())

		timeout := time.NewTimer(5 * time.Second)
		for {
			select {
			case <-timeout.C:
				difference := time.Since(h.lastUpdate.Load())
				log.Warn().Msgf("No head updates of upstream %s for %d ms", h.upstreamId, difference.Milliseconds())
				h.head.OnNoHeadUpdates()
			case <-ctx.Done():
				return nil
			case block, ok := <-h.head.HeadsChan():
				if ok {
					log.Debug().Msgf("got a new head of upstream %s - %d", h.upstreamId, block.BlockData.Height)
					h.lastUpdate.Store(time.Now())
					h.subManager.Publish(HeadEvent{HeadData: block.BlockData})
				}
			case manualBlock := <-h.manualHeadChan:
				if manualBlock.BlockData.Height > h.head.GetCurrentBlock().BlockData.Height {
					log.Debug().Msgf("got a new manual head of upstream %s - %d", h.upstreamId, manualBlock.BlockData.Height)
					h.lastUpdate.Store(time.Now())
					h.head.UpdateHead(*manualBlock)
					h.subManager.Publish(HeadEvent{HeadData: manualBlock.BlockData})
				}
			}
			timeout.Reset(h.headNoUpdatesTimeout)
		}
	})
}

func (h *HeadProcessor) Stop() {
	h.lifecycle.Stop()
	h.head.Stop()
}

func (h *HeadProcessor) UpdateHead(height, slot uint64) {
	h.manualHeadChan <- protocol.NewBlock(height, slot, "")
}

func createHead(
	ctx context.Context,
	id string, pollInterval time.Duration,
	apiConnector connectors.ApiConnector,
	specific specific.ChainSpecific,
	options *config.UpstreamOptions,
) Head {
	switch apiConnector.GetType() {
	case protocol.JsonRpcConnector, protocol.RestConnector:
		log.Info().Msgf("starting an rpc head of upstream %s with poll interval %s", id, pollInterval)
		return newRpcHead(ctx, id, apiConnector, specific, pollInterval, options)
	case protocol.WsConnector:
		log.Info().Msgf("starting a subscription head of upstream %s", id)
		return newWsHead(ctx, id, apiConnector, specific)
	default:
		return nil
	}
}

type Head interface {
	utils.Lifecycle
	HeadsChan() chan *protocol.Block
	OnNoHeadUpdates()
	GetCurrentBlock() *protocol.Block
	UpdateHead(newHead protocol.Block)
}

type RpcHead struct {
	lifecycle       *utils.BaseLifecycle
	block           *utils.Atomic[protocol.Block]
	chainSpecific   specific.ChainSpecific
	pollInterval    time.Duration
	internalTimeout time.Duration
	connector       connectors.ApiConnector
	upstreamId      string
	pollInProgress  atomic.Bool
	headsChan       chan *protocol.Block
}

func (r *RpcHead) Running() bool {
	return r.lifecycle.Running()
}

func (r *RpcHead) Stop() {
	log.Info().Msgf("stopping an rpc head of upstream '%s'", r.upstreamId)
	r.lifecycle.Stop()
}

func (r *RpcHead) UpdateHead(newHead protocol.Block) {
	r.block.Store(newHead)
}

var _ Head = (*RpcHead)(nil)

func newRpcHead(
	ctx context.Context,
	upstreamId string,
	connector connectors.ApiConnector,
	chainSpecific specific.ChainSpecific,
	pollInterval time.Duration,
	options *config.UpstreamOptions,
) *RpcHead {
	head := RpcHead{
		lifecycle:       utils.NewBaseLifecycle(fmt.Sprintf("%s_rpc_head", upstreamId), ctx),
		block:           utils.NewAtomic[protocol.Block](),
		chainSpecific:   chainSpecific,
		pollInterval:    pollInterval,
		connector:       connector,
		upstreamId:      upstreamId,
		pollInProgress:  atomic.Bool{},
		headsChan:       make(chan *protocol.Block),
		internalTimeout: options.InternalTimeout,
	}

	return &head
}

func (r *RpcHead) Start() {
	r.lifecycle.Start(func(ctx context.Context) error {
		for {
			r.poll()
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(r.pollInterval):
			}
		}
	})
}

func (r *RpcHead) GetCurrentBlock() *protocol.Block {
	block := r.block.Load()
	return &block
}

func (r *RpcHead) HeadsChan() chan *protocol.Block {
	return r.headsChan
}

func (r *RpcHead) OnNoHeadUpdates() {
}

func (r *RpcHead) poll() {
	if !r.pollInProgress.Load() {
		r.pollInProgress.Store(true)
		defer r.pollInProgress.Store(false)

		ctx, cancel := context.WithTimeout(r.lifecycle.GetParentContext(), r.internalTimeout)
		defer cancel()

		block, err := r.chainSpecific.GetLatestBlock(ctx, r.connector)
		if err != nil {
			log.Warn().Err(err).Msgf("couldn't get the latest block of upstream %s", r.upstreamId)
		} else {
			r.block.Store(*block)
			r.headsChan <- block
		}
	}
}

type SubscriptionHead struct {
	lifecycle     *utils.BaseLifecycle
	block         *utils.Atomic[protocol.Block]
	chainSpecific specific.ChainSpecific
	connector     connectors.ApiConnector
	upstreamId    string
	headsChan     chan *protocol.Block
}

func (w *SubscriptionHead) Running() bool {
	return w.lifecycle.Running()
}

func (w *SubscriptionHead) Stop() {
	log.Info().Msgf("stopping subscription head of upstream '%s'", w.upstreamId)
	w.lifecycle.Stop()
}

func (w *SubscriptionHead) UpdateHead(newHead protocol.Block) {
	w.block.Store(newHead)
}

var _ Head = (*SubscriptionHead)(nil)

func (w *SubscriptionHead) GetCurrentBlock() *protocol.Block {
	block := w.block.Load()
	return &block
}

func (w *SubscriptionHead) Start() {
	w.lifecycle.Start(func(ctx context.Context) error {
		subReq, err := w.chainSpecific.SubscribeHeadRequest()
		if err != nil {
			log.Warn().Err(err).Msgf("couldn't create a subscription request to upstream %s", w.upstreamId)
			return err
		}

		subResponse, err := w.connector.Subscribe(ctx, subReq)
		if err != nil {
			log.Warn().Err(err).Msgf("couldn't subscribe to upstream %s heads", w.upstreamId)
			return err
		}
		for {
			select {
			case message, ok := <-subResponse.ResponseChan():
				if !ok {
					return nil
				}
				if message.Error != nil {
					log.Warn().Err(message.Error).Msgf("got an error from heads subscription of upstream %s", w.upstreamId)
					return nil
				}
				if message.Type == protocol.Ws {
					block, err := w.chainSpecific.ParseSubscriptionBlock(message.Message)
					if err != nil {
						log.Warn().Err(err).Msgf("couldn't parse a message from heads subscription of upstream %s", w.upstreamId)
						return nil
					}
					w.block.Store(*block)
					w.headsChan <- block
				}
			case <-ctx.Done():
				return nil
			}
		}
	})
}

func (w *SubscriptionHead) HeadsChan() chan *protocol.Block {
	return w.headsChan
}

func (w *SubscriptionHead) OnNoHeadUpdates() {
	log.Info().Msgf("trying to resubscribe to new heads of upstream %s", w.upstreamId)
	w.Stop()
	go w.Start()
}

func newWsHead(ctx context.Context, upstreamId string, connector connectors.ApiConnector, chainSpecific specific.ChainSpecific) *SubscriptionHead {
	head := SubscriptionHead{
		lifecycle:     utils.NewBaseLifecycle(fmt.Sprintf("%s_subscription_head", upstreamId), ctx),
		upstreamId:    upstreamId,
		chainSpecific: chainSpecific,
		connector:     connector,
		block:         utils.NewAtomic[protocol.Block](),
		headsChan:     make(chan *protocol.Block),
	}

	return &head
}
