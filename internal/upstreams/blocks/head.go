package blocks

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

type Head interface {
	utils.Lifecycle
	HeadsChan() chan protocol.Block
	OnNoHeadUpdates()
	GetCurrentBlock() protocol.Block
	UpdateHead(newHead protocol.Block)
}

type RpcHead struct {
	lifecycle       *utils.BaseLifecycle
	block           *utils.Atomic[protocol.Block]
	chainSpecific   specific.ChainSpecific
	pollInterval    time.Duration
	internalTimeout time.Duration
	upstreamId      string
	pollInProgress  atomic.Bool
	headsChan       chan protocol.Block
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
	internalTimeout,
	pollInterval time.Duration,
	chainSpecific specific.ChainSpecific,
) *RpcHead {
	head := RpcHead{
		lifecycle:       utils.NewBaseLifecycle(fmt.Sprintf("%s_rpc_head", upstreamId), ctx),
		block:           utils.NewAtomic[protocol.Block](),
		chainSpecific:   chainSpecific,
		pollInterval:    pollInterval,
		upstreamId:      upstreamId,
		pollInProgress:  atomic.Bool{},
		headsChan:       make(chan protocol.Block),
		internalTimeout: internalTimeout,
	}

	return &head
}

func (r *RpcHead) Start() {
	log.Info().Msgf("starting an rpc head of upstream %s with poll interval %s", r.upstreamId, r.pollInterval)
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

func (r *RpcHead) GetCurrentBlock() protocol.Block {
	block := r.block.Load()
	return block
}

func (r *RpcHead) HeadsChan() chan protocol.Block {
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

		block, err := r.chainSpecific.GetLatestBlock(ctx)
		if err != nil {
			log.Error().Err(err).Msgf("couldn't get the latest block of upstream %s", r.upstreamId)
		} else {
			r.block.Store(block)
			r.headsChan <- block
		}
	}
}

type SubscriptionHead struct {
	lifecycle       *utils.BaseLifecycle
	block           *utils.Atomic[protocol.Block]
	chainSpecific   specific.ChainSpecific
	headConnector   connectors.ApiConnector
	upstreamId      string
	headsChan       chan protocol.Block
	internalTimeout time.Duration
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

func (w *SubscriptionHead) GetCurrentBlock() protocol.Block {
	block := w.block.Load()
	return block
}

func (w *SubscriptionHead) Start() {
	log.Info().Msgf("starting a subscription head of upstream %s", w.upstreamId)
	w.lifecycle.Start(func(ctx context.Context) error {
		// get the latest block in order not to wait for the sub event
		w.getLatestBlock()

		subReq, err := w.chainSpecific.SubscribeHeadRequest()
		if err != nil {
			log.Error().Err(err).Msgf("couldn't create a subscription request to upstream %s", w.upstreamId)
			return err
		}

		subResponse, err := w.headConnector.Subscribe(ctx, subReq)
		if err != nil {
			log.Error().Err(err).Msgf("couldn't subscribe to upstream %s heads", w.upstreamId)
			return err
		}
		for {
			select {
			case message, ok := <-subResponse.ResponseChan():
				if !ok {
					return nil
				}
				if message.Error != nil {
					log.Error().Err(message.Error).Msgf("got an error from heads subscription of upstream %s", w.upstreamId)
					return nil
				}
				if message.Type == protocol.Ws {
					block, err := w.chainSpecific.ParseSubscriptionBlock(message.Message)
					if err != nil {
						log.Error().Err(err).Msgf("couldn't parse a message from heads subscription of upstream %s", w.upstreamId)
						return nil
					}
					w.block.Store(block)
					w.headsChan <- block
				}
			case <-ctx.Done():
				return nil
			}
		}
	})
}

func (w *SubscriptionHead) HeadsChan() chan protocol.Block {
	return w.headsChan
}

func (w *SubscriptionHead) OnNoHeadUpdates() {
	log.Info().Msgf("trying to resubscribe to new heads of upstream %s", w.upstreamId)
	w.Stop()
	go w.Start()
}

func (w *SubscriptionHead) getLatestBlock() {
	ctx, cancel := context.WithTimeout(w.lifecycle.GetParentContext(), w.internalTimeout)
	defer cancel()
	block, err := w.chainSpecific.GetLatestBlock(ctx)
	if err != nil {
		log.Error().Err(err).Msgf("couldn't get the latest block of upstream %s", w.upstreamId)
		return
	}
	w.block.Store(block)
	w.headsChan <- block
}

func newWsHead(
	ctx context.Context,
	upstreamId string,
	internalTimeout time.Duration,
	headConnector connectors.ApiConnector,
	chainSpecific specific.ChainSpecific,
) *SubscriptionHead {
	head := SubscriptionHead{
		lifecycle:       utils.NewBaseLifecycle(fmt.Sprintf("%s_subscription_head", upstreamId), ctx),
		upstreamId:      upstreamId,
		chainSpecific:   chainSpecific,
		headConnector:   headConnector,
		internalTimeout: internalTimeout,
		block:           utils.NewAtomic[protocol.Block](),
		headsChan:       make(chan protocol.Block),
	}

	return &head
}
