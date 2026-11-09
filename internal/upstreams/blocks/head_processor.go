package blocks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/drpcorg/public/pkg/methods"
	"github.com/rs/zerolog/log"
)

type HeadProcessor interface {
	utils.Lifecycle

	GetCurrentBlock() protocol.Block
	UpdateHead(height, slot uint64)

	Subscribe(name string) *utils.Subscription[HeadEvent]
	// SubscribeWithReplay also delivers the last published event first. On this stream that
	// event is HeadStateEvent{Running: false} exactly when the head is stopped - Stop
	// publishes it last and nothing else is published until Start - so a late subscriber
	// learns whether the head is currently paused.
	SubscribeWithReplay(name string) *utils.Subscription[HeadEvent]
}

// HeadEvent is what a head processor publishes to its subscribers: head blocks interleaved
// with lifecycle changes, in the order they happened. One stream keeps them ordered, so a
// consumer can tell a block that arrived before a stop from one that arrived after a start.
type HeadEvent interface {
	headEvent()
}

// HeadBlockEvent carries a new head of the upstream.
type HeadBlockEvent struct {
	HeadData protocol.Block
}

// HeadStateEvent reports that the head processor started or stopped observing the node.
// Silence after Running == false is not a stall: nothing is being observed.
type HeadStateEvent struct {
	Running bool
}

// HeadFatalEvent reports that the head guard found the node on another chain: the head
// that revealed it was dropped, and the upstream must be removed.
type HeadFatalEvent struct{}

func (HeadBlockEvent) headEvent() {}

func (HeadStateEvent) headEvent() {}

func (HeadFatalEvent) headEvent() {}

// headJumpThreshold is the height distance from the last published head past which a
// head is suspicious and is checked against the chain before it is published.
const headJumpThreshold = 10000

type GenericHeadProcessor struct {
	upstreamId           string
	lifecycle            *utils.GenericLifecycle
	head                 Head
	lastUpdate           *utils.Atomic[time.Time]
	headNoUpdatesTimeout time.Duration
	subManager           *utils.SubscriptionManager[HeadEvent]
	manualHeadChan       chan protocol.Block
	// chainValidator vets a suspicious head; nil turns the guard off.
	chainValidator validations.SettingsValidator
	// lastPublished is the last block this run published, head or manual; it is the
	// reference for the jump guard and for manual heads. The head's own current block is
	// not, because the head stores a block before sending it, so a dropped one would sit
	// there. Cleared on every Start.
	lastPublished *utils.Atomic[protocol.Block]
}

func NewGenericHeadProcessor(
	ctx context.Context,
	upConfig *config.Upstream,
	headConnector connectors.ApiConnector,
	specific BlockChainSpecific,
	chainValidator validations.SettingsValidator,
) *GenericHeadProcessor {
	configuredChain := chains.GetChain(upConfig.ChainName)
	head := createHead(ctx, upConfig.Id, upConfig.PollInterval, upConfig.HeadMode, headConnector, specific, upConfig.Options)

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
	return &GenericHeadProcessor{
		upstreamId:           upConfig.Id,
		head:                 head,
		manualHeadChan:       make(chan protocol.Block, 100),
		lifecycle:            utils.NewGenericLifecycle(name, ctx),
		headNoUpdatesTimeout: headNoUpdatesTimeout,
		lastUpdate:           utils.NewAtomic[time.Time](),
		lastPublished:        utils.NewAtomic[protocol.Block](),
		subManager:           utils.NewSubscriptionManager[HeadEvent](name),
		chainValidator:       chainValidator,
	}
}

func (h *GenericHeadProcessor) GetCurrentBlock() protocol.Block {
	return h.head.GetCurrentBlock()
}

func (h *GenericHeadProcessor) Subscribe(name string) *utils.Subscription[HeadEvent] {
	return h.subManager.Subscribe(name)
}

func (h *GenericHeadProcessor) SubscribeWithReplay(name string) *utils.Subscription[HeadEvent] {
	return h.subManager.SubscribeWithReplay(name)
}

func (h *GenericHeadProcessor) Running() bool {
	return h.lifecycle.Running()
}

// Start runs the head and drains its blocks into the subscribers. The teardown it returns
// waits for the drain goroutine before publishing the stop, so nothing of this run is
// published after HeadStateEvent{Running: false}: a late subscriber's replay then reports a
// paused head exactly when the head is paused.
//
// The wait is short. A silence nudge (Head.OnNoHeadUpdates) is a non-blocking signal to the
// head, and the one network call the drain makes, the chain check of a suspicious head, is
// abandoned as soon as the run context ends: the drain selects on it and discards the
// verdict, so Stop never waits for a validator.
func (h *GenericHeadProcessor) Start() {
	h.lifecycle.Start(func(ctx context.Context) (func(), error) {
		h.lastPublished.Store(protocol.ZeroBlock{})
		h.head.Start()
		h.lastUpdate.Store(time.Now())
		h.subManager.Publish(HeadStateEvent{Running: true})

		drainDone := make(chan struct{})
		go func() {
			defer close(drainDone)
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
						// a dropped block still counts as life: the node is up, it is just wrong
						h.lastUpdate.Store(time.Now())
						if h.admit(ctx, block) {
							h.publish(block)
						}
					}
				case manualBlock := <-h.manualHeadChan:
					if manualBlock.Height > h.lastPublished.Load().Height {
						log.Debug().Msgf("got a new manual head of upstream %s - %d", h.upstreamId, manualBlock.Height)
						h.lastUpdate.Store(time.Now())
						h.head.UpdateHead(manualBlock)
						h.publish(manualBlock)
					}
				}
				timeout.Reset(h.headNoUpdatesTimeout)
			}
		}()
		return func() {
			<-drainDone
			h.head.Stop()
			h.subManager.Publish(HeadStateEvent{Running: false})
		}, nil
	})
}

func (h *GenericHeadProcessor) Stop() {
	h.lifecycle.Stop()
}

func (h *GenericHeadProcessor) UpdateHead(height, slot uint64) {
	select {
	case h.manualHeadChan <- protocol.NewBlockWithHeights(height, slot):
	default:
		// nobody drains the channel while the head is paused; a dropped manual head
		// costs nothing, the next real head supersedes it
		log.Debug().Msgf("dropped a manual head %d of upstream %s: the head processor is not draining", height, h.upstreamId)
	}
}

func (h *GenericHeadProcessor) publish(block protocol.Block) {
	h.lastPublished.Store(block)
	h.subManager.Publish(HeadBlockEvent{HeadData: block})
}

// admit decides whether a head block may be published. A block whose height jumped past
// headJumpThreshold from the last published one is checked against the chain first: a
// node that silently switched chains, or an endpoint that started answering for another
// network, is caught here rather than fed into the chain state.
func (h *GenericHeadProcessor) admit(ctx context.Context, block protocol.Block) bool {
	if !h.isSuspicious(block) {
		return true
	}
	log.Warn().Msgf(
		"suspicious head %d of upstream %s is far from the last published head %d, checking the chain",
		block.Height, h.upstreamId, h.lastPublished.Load().Height,
	)
	result, ok := h.checkChain(ctx)
	if !ok {
		return false
	}
	switch result {
	case validations.Valid:
		log.Info().Msgf("head %d of upstream %s comes from the configured chain, accepting it", block.Height, h.upstreamId)
		return true
	case validations.FatalSettingError:
		log.Error().Msgf("head %d of upstream %s comes from another chain, dropping it and removing the upstream", block.Height, h.upstreamId)
		h.subManager.Publish(HeadFatalEvent{})
		return false
	default:
		log.Warn().Msgf("chain check of upstream %s is inconclusive, dropping head %d", h.upstreamId, block.Height)
		return false
	}
}

func (h *GenericHeadProcessor) isSuspicious(block protocol.Block) bool {
	if h.chainValidator == nil {
		return false
	}
	last := h.lastPublished.Load()
	if last.IsFullEmpty() {
		return false
	}
	distance := block.Height - last.Height
	if last.Height > block.Height {
		distance = last.Height - block.Height
	}
	return distance > headJumpThreshold
}

// checkChain runs the chain validator and waits for its verdict or for the run to end,
// whichever comes first. On a cancelled run the verdict is discarded and the validator
// call finishes on its own within its own timeout; the second value is false then.
func (h *GenericHeadProcessor) checkChain(ctx context.Context) (validations.ValidationSettingResult, bool) {
	verdict := make(chan validations.ValidationSettingResult, 1)
	go func() {
		verdict <- h.chainValidator.Validate()
	}()
	select {
	case result := <-verdict:
		return result, true
	case <-ctx.Done():
		return validations.UnknownResult, false
	}
}

func createHead(
	ctx context.Context,
	id string, pollInterval time.Duration,
	headMode config.HeadMode,
	headConnector connectors.ApiConnector,
	specific BlockChainSpecific,
	options *chains.Options,
) Head {
	switch headConnector.GetType() {
	case specs.JsonRpcConnector, specs.TendermintConnector, specs.RestConnector, specs.RestIndexer:
		return NewRpcHead(ctx, id, options.InternalTimeout, pollInterval, specific)
	case specs.WebsocketConnector:
		return NewSubHead(ctx, id, options.InternalTimeout, headConnector, specific)
	case specs.GrpcConnector:
		return createGrpcHead(ctx, id, pollInterval, headMode, headConnector, specific, options)
	default:
		return nil
	}
}

// createGrpcHead honours head-mode: a gRPC upstream can either poll or hold a
// head subscription. A chain that cannot push heads (the sentinel) is polled
// with a warning; any other error from SubscribeHeadRequest is a real failure
// and is left to the subscription head, which fails loudly on Start.
func createGrpcHead(
	ctx context.Context,
	id string, pollInterval time.Duration,
	headMode config.HeadMode,
	headConnector connectors.ApiConnector,
	specific BlockChainSpecific,
	options *chains.Options,
) Head {
	if headMode == config.HeadModePoll {
		return NewRpcHead(ctx, id, options.InternalTimeout, pollInterval, specific)
	}
	if _, err := specific.SubscribeHeadRequest(); errors.Is(err, ErrUnsupportedHeadSubscriptions) {
		log.Warn().Msgf("upstream %s: the chain does not support head subscriptions, falling back to head polling", id)
		return NewRpcHead(ctx, id, options.InternalTimeout, pollInterval, specific)
	}
	return NewSubHead(ctx, id, options.InternalTimeout, headConnector, specific)
}

var _ HeadProcessor = (*GenericHeadProcessor)(nil)
