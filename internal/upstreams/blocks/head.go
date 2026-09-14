package blocks

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

// ErrUnsupportedHeadSubscriptions is returned by SubscribeHeadRequest and
// ParseSubscriptionBlock of every chain that cannot push heads. createHead
// recognizes it (errors.Is) to fall back to polling; any other error is a real
// failure.
var ErrUnsupportedHeadSubscriptions = errors.New("head subscriptions are not supported")

type BlockChainSpecific interface {
	GetLatestBlock(ctx context.Context) (protocol.Block, error)
	GetFinalizedBlock(context.Context) (protocol.Block, error)

	ParseBlock([]byte) (protocol.Block, error)
	ParseSubscriptionBlock(data []byte) (protocol.Block, error)

	SubscribeHeadRequest() (protocol.RequestHolder, error)
}

type Head interface {
	utils.Lifecycle
	HeadsChan() chan protocol.Block
	// OnNoHeadUpdates is the processor's silence nudge: no head arrived within the expected
	// window. It must return at once and must not touch the head's lifecycle; the processor
	// is the only owner of Start and Stop.
	OnNoHeadUpdates()
	GetCurrentBlock() protocol.Block
	UpdateHead(newHead protocol.Block)
}

type RpcHead struct {
	lifecycle       *utils.GenericLifecycle
	block           *utils.Atomic[protocol.Block]
	chainSpecific   BlockChainSpecific
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

func NewRpcHead(
	ctx context.Context,
	upstreamId string,
	internalTimeout,
	pollInterval time.Duration,
	chainSpecific BlockChainSpecific,
) *RpcHead {
	head := RpcHead{
		lifecycle:       utils.NewGenericLifecycle(fmt.Sprintf("%s_rpc_head", upstreamId), ctx),
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
		go func() {
			for {
				r.poll(ctx)
				select {
				case <-ctx.Done():
					return
				case <-time.After(r.pollInterval):
				}
			}
		}()
		return nil
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

// poll fetches the latest block and hands it to the processor. The send waits for the
// reader only while this run of the head is alive: once it is stopped nobody drains the
// channel, and a poll parked in the send would hold the poll slot and deliver a stale block
// on the next start.
func (r *RpcHead) poll(runCtx context.Context) {
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
			select {
			case r.headsChan <- block:
			case <-runCtx.Done():
			}
		}
	}
}

type SubscriptionHead struct {
	lifecycle       *utils.GenericLifecycle
	block           *utils.Atomic[protocol.Block]
	chainSpecific   BlockChainSpecific
	headConnector   connectors.ApiConnector
	upstreamId      string
	headsChan       chan protocol.Block
	internalTimeout time.Duration
	// resubscribe carries the processor's silence nudges to the run goroutine. Capacity one
	// and a non-blocking send: a second nudge while one is pending adds nothing.
	resubscribe chan struct{}
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

// Start launches the run goroutine and returns at once: the subscribe happens on the
// goroutine, under the run's context, so a wedged socket never holds the caller.
func (w *SubscriptionHead) Start() {
	log.Info().Msgf("starting a subscription head of upstream %s", w.upstreamId)
	w.lifecycle.Start(func(ctx context.Context) error {
		go w.run(ctx)
		return nil
	})
}

func (w *SubscriptionHead) HeadsChan() chan protocol.Block {
	return w.headsChan
}

// OnNoHeadUpdates asks the run goroutine to drop the current subscription and open a new
// one. It never touches the lifecycle: the processor is the only owner of that.
func (w *SubscriptionHead) OnNoHeadUpdates() {
	select {
	case w.resubscribe <- struct{}{}:
	default:
	}
}

var errResubscribeRequested = errors.New("resubscribe requested")

// run owns the subscription for the whole life of the run: one subscription at a time,
// replaced on a nudge, and retried after a failure only on the next nudge - the cadence the
// processor's silence timer provides.
func (w *SubscriptionHead) run(ctx context.Context) {
	for {
		err := w.serveSubscription(ctx)
		if ctx.Err() != nil {
			return
		}
		if !errors.Is(err, errResubscribeRequested) {
			log.Error().Err(err).Msgf("heads subscription of upstream %s is down, waiting for a resubscribe", w.upstreamId)
			select {
			case <-ctx.Done():
				return
			case <-w.resubscribe:
			}
		}
		log.Info().Msgf("resubscribing to new heads of upstream %s", w.upstreamId)
	}
}

// serveSubscription opens one subscription and forwards its heads until the stream ends,
// a resubscribe is requested, or the run is cancelled. The subscription is always released
// on the way out, so a cancelled run leaves nothing open on the connector.
func (w *SubscriptionHead) serveSubscription(ctx context.Context) error {
	subReq, err := w.chainSpecific.SubscribeHeadRequest()
	if err != nil {
		return err
	}
	subResponse, err := w.headConnector.Subscribe(ctx, subReq)
	if err != nil {
		return err
	}
	defer w.headConnector.Unsubscribe(subResponse.OpId())

	// get the latest block in order not to wait for the sub event
	w.getLatestBlock(ctx)
	for {
		select {
		case message, ok := <-subResponse.ResponseChan():
			if !ok {
				return errors.New("heads subscription closed")
			}
			if message.GetError() != nil {
				return message.GetError()
			}
			if message.IsEnd() {
				return errors.New("heads subscription ended")
			}
			block, err := w.chainSpecific.ParseSubscriptionBlock(message.GetMessage())
			if err != nil {
				return err
			}
			w.block.Store(block)
			// the reader is gone once this run is stopped; a send parked here
			// would deliver this block on the next start as if it were fresh
			select {
			case w.headsChan <- block:
			case <-ctx.Done():
				return nil
			}
		case <-w.resubscribe:
			return errResubscribeRequested
		case <-ctx.Done():
			return nil
		}
	}
}

func (w *SubscriptionHead) getLatestBlock(runCtx context.Context) {
	ctx, cancel := context.WithTimeout(w.lifecycle.GetParentContext(), w.internalTimeout)
	defer cancel()
	block, err := w.chainSpecific.GetLatestBlock(ctx)
	if err != nil {
		log.Error().Err(err).Msgf("couldn't get the latest block of upstream %s", w.upstreamId)
		return
	}
	w.block.Store(block)
	select {
	case w.headsChan <- block:
	case <-runCtx.Done():
	}
}

func NewSubHead(
	ctx context.Context,
	upstreamId string,
	internalTimeout time.Duration,
	headConnector connectors.ApiConnector,
	chainSpecific BlockChainSpecific,
) *SubscriptionHead {
	head := SubscriptionHead{
		lifecycle:       utils.NewGenericLifecycle(fmt.Sprintf("%s_subscription_head", upstreamId), ctx),
		upstreamId:      upstreamId,
		chainSpecific:   chainSpecific,
		headConnector:   headConnector,
		internalTimeout: internalTimeout,
		block:           utils.NewAtomic[protocol.Block](),
		headsChan:       make(chan protocol.Block),
		resubscribe:     make(chan struct{}, 1),
	}

	return &head
}
