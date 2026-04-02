package blocks

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

type HeadProcessor interface {
	utils.Lifecycle

	GetCurrentBlock() protocol.Block
	UpdateHead(height, slot uint64)

	Subscribe(name string) *utils.Subscription[HeadEvent]
}

type HeadEvent struct {
	HeadData protocol.Block
}

type BaseHeadProcessor struct {
	upstreamId           string
	lifecycle            *utils.BaseLifecycle
	head                 Head
	lastUpdate           *utils.Atomic[time.Time]
	headNoUpdatesTimeout time.Duration
	subManager           *utils.SubscriptionManager[HeadEvent]
	manualHeadChan       chan protocol.Block
}

func NewBaseHeadProcessor(
	ctx context.Context,
	upConfig *config.Upstream,
	headConnector connectors.ApiConnector,
	specific specific.ChainSpecific,
) *BaseHeadProcessor {
	configuredChain := chains.GetChain(upConfig.ChainName)
	head := createHead(ctx, upConfig.Id, upConfig.PollInterval, headConnector, specific, upConfig.Options)

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
	return &BaseHeadProcessor{
		upstreamId:           upConfig.Id,
		head:                 head,
		manualHeadChan:       make(chan protocol.Block, 100),
		lifecycle:            utils.NewBaseLifecycle(name, ctx),
		headNoUpdatesTimeout: headNoUpdatesTimeout,
		lastUpdate:           utils.NewAtomic[time.Time](),
		subManager:           utils.NewSubscriptionManager[HeadEvent](name),
	}
}

func (h *BaseHeadProcessor) GetCurrentBlock() protocol.Block {
	return h.head.GetCurrentBlock()
}

func (h *BaseHeadProcessor) Subscribe(name string) *utils.Subscription[HeadEvent] {
	return h.subManager.Subscribe(name)
}

func (h *BaseHeadProcessor) Running() bool {
	return h.lifecycle.Running()
}

func (h *BaseHeadProcessor) Start() {
	h.lifecycle.Start(func(ctx context.Context) error {
		h.head.Start()
		h.lastUpdate.Store(time.Now())

		go func() {
			timeout := time.NewTimer(h.headNoUpdatesTimeout)
			for {
				select {
				case <-timeout.C:
					difference := time.Since(h.lastUpdate.Load())
					log.Warn().Msgf("No head updates of upstream %s for %d ms", h.upstreamId, difference.Milliseconds())
					h.head.OnNoHeadUpdates()
				case <-ctx.Done():
					return
				case block, ok := <-h.head.HeadsChan():
					if ok {
						log.Debug().Msgf("got a new head of upstream %s - %d", h.upstreamId, block.Height)
						h.lastUpdate.Store(time.Now())
						h.subManager.Publish(HeadEvent{HeadData: block})
					}
				case manualBlock := <-h.manualHeadChan:
					if manualBlock.Height > h.head.GetCurrentBlock().Height {
						log.Debug().Msgf("got a new manual head of upstream %s - %d", h.upstreamId, manualBlock.Height)
						h.lastUpdate.Store(time.Now())
						h.head.UpdateHead(manualBlock)
						h.subManager.Publish(HeadEvent{HeadData: manualBlock})
					}
				}
				timeout.Reset(h.headNoUpdatesTimeout)
			}
		}()
		return nil
	})
}

func (h *BaseHeadProcessor) Stop() {
	h.lifecycle.Stop()
	h.head.Stop()
}

func (h *BaseHeadProcessor) UpdateHead(height, slot uint64) {
	h.manualHeadChan <- protocol.NewBlockWithHeights(height, slot)
}

func createHead(
	ctx context.Context,
	id string, pollInterval time.Duration,
	headConnector connectors.ApiConnector,
	specific specific.ChainSpecific,
	options *config.UpstreamOptions,
) Head {
	switch headConnector.GetType() {
	case protocol.JsonRpcConnector, protocol.RestConnector:
		return newRpcHead(ctx, id, options.InternalTimeout, pollInterval, specific)
	case protocol.WsConnector:
		return newWsHead(ctx, id, options.InternalTimeout, headConnector, specific)
	default:
		return nil
	}
}

var _ HeadProcessor = (*BaseHeadProcessor)(nil)
