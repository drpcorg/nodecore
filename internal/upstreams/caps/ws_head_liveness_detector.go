package caps

import (
	"context"
	"fmt"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

const (
	// checkedBlocksUntilLive is how many blocks in a row must be observed before a head is
	// considered live. N blocks == N-1 consecutive height increments.
	checkedBlocksUntilLive = 3
	// cooldown is how long after a non-consecutive (forward gap) event the head stays not-live,
	// even once consecutive blocks resume and the block-count threshold is met again.
	cooldown = 5 * time.Minute
	// timeoutMultiplier scales measuredBlockTime for the stall-timeout window and is also the
	// backoff factor applied to measuredBlockTime whenever a timeout fires.
	timeoutMultiplier = 2
	// maxBlockTime caps how large measuredBlockTime can grow via timeout backoff.
	maxBlockTime = 10 * time.Minute
	// defaultBlockTime seeds measuredBlockTime when the chain has no configured expected block
	// time, so the stall timeout still has a sane window.
	defaultBlockTime = 12 * time.Second
)

// HeadSource is the subset of blocks.HeadProcessor the detector consumes: a stream of new
// heads. blocks.HeadProcessor satisfies it.
type HeadSource interface {
	Subscribe(name string) *utils.Subscription[blocks.HeadEvent]
}

// headLivenessTracker reduces a stream of head heights into a "live" verdict The head goes live once it has produced
// checkedBlocksUntilLive blocks in a row AND is not in the post-gap cooldown. A forward gap
// (diff >= 2) resets the run and starts the cooldown; duplicate heights and backward reorgs
// (diff <= 1) are treated as consecutive. A stall - no head progress within timeout() - is
// reported via onTimeout, which retracts liveness and backs off measuredBlockTime.
type headLivenessTracker struct {
	upstreamId string
	lastHeight uint64
	haveLast   bool
	count      int
	live       bool

	// measuredBlockTime is the current estimate of the inter-block interval. It starts at the
	// chain's expected block time, grows to the largest observed interval, and is doubled
	// (capped at maxBlockTime) each time a stall timeout fires. It drives the timeout window.
	measuredBlockTime time.Duration
	lastBlockTime     time.Time
	haveLastBlockTime bool

	// lastNonConsecutiveTime is when the last non-consecutive (forward gap) event happened. Its
	// zero value (before any gap) makes now.Sub(it) saturate well past cooldown, so the first
	// run is never held back - no separate "have we seen a gap" flag is needed.
	lastNonConsecutiveTime time.Time

	now func() time.Time
}

func (h *headLivenessTracker) observe(height uint64) bool {
	now := h.now()

	// Measure the actual inter-block interval and let measuredBlockTime grow to the largest one
	// seen (never shrink), so the stall timeout adapts to slow chains without false positives.
	if h.haveLastBlockTime {
		if interval := now.Sub(h.lastBlockTime); interval > h.measuredBlockTime {
			log.Info().Msgf("updated measured block time to %d ms of upstream '%s'", interval.Milliseconds(), h.upstreamId)
			h.measuredBlockTime = interval
		}
	}
	h.lastBlockTime = now
	h.haveLastBlockTime = true

	if !h.haveLast {
		h.haveLast = true
		h.lastHeight = height
		return h.live
	}

	diff := int64(height) - int64(h.lastHeight)
	if diff <= 1 {
		h.count++
		if h.count >= checkedBlocksUntilLive-1 && now.Sub(h.lastNonConsecutiveTime) >= cooldown {
			h.live = true
		}
	} else {
		// a forward gap (diff >= 2): the head skipped blocks - reset the run and start the
		// cooldown before it may be considered live again.
		if h.live {
			log.
				Warn().
				Msgf(
					"non consecutive blocks in head of upstream '%s', last height - %d, current height - %d, diff - %d",
					h.upstreamId,
					h.lastHeight,
					height,
					diff,
				)
		}
		h.count = 0
		h.live = false
		h.lastNonConsecutiveTime = now
	}
	h.lastHeight = height
	return h.live
}

// onTimeout records a stall: no head advanced within the timeout window. It retracts liveness
// and backs off measuredBlockTime (capped at maxBlockTime) so the next window is longer, then
// returns the (now false) verdict.
func (h *headLivenessTracker) onTimeout() bool {
	log.Warn().Msgf("head liveness check failed with timeout %d ms of upstream '%s'", h.measuredBlockTime.Milliseconds(), h.upstreamId)
	h.live = false
	h.measuredBlockTime = min(h.measuredBlockTime*timeoutMultiplier, maxBlockTime)
	return h.live
}

// timeout is the stall window: how long the detector waits for head progress before calling onTimeout.
func (h *headLivenessTracker) timeout() time.Duration {
	return h.measuredBlockTime * checkedBlocksUntilLive * timeoutMultiplier
}

// WsHeadLivenessCapDetector asserts a single cap while the given websocket connector is
// connected AND its head is advancing, and retracts it otherwise. It is used to gate WsCap on
// ws-driven EVM heads: a flapping or stalled head (missed blocks / no progress) pulls the
// upstream out of subscription serving without affecting regular RPC routing. If the connector
// isn't a websocket (SubscribeStates returns nil) or there is no head source, the cap is never
// asserted.
type WsHeadLivenessCapDetector struct {
	upstreamId        string
	name              string
	cap               protocol.Cap
	wsConn            connectors.ApiConnector
	head              HeadSource
	expectedBlockTime time.Duration
	now               func() time.Time
}

func NewWsHeadLivenessCapDetector(upstreamId, name string, cap protocol.Cap, wsConn connectors.ApiConnector, head HeadSource, expectedBlockTime time.Duration) *WsHeadLivenessCapDetector {
	if expectedBlockTime <= 0 {
		expectedBlockTime = defaultBlockTime
	}
	return &WsHeadLivenessCapDetector{
		upstreamId:        upstreamId,
		name:              name,
		cap:               cap,
		wsConn:            wsConn,
		head:              head,
		expectedBlockTime: expectedBlockTime,
		now:               time.Now,
	}
}

func (d *WsHeadLivenessCapDetector) Domain() []protocol.Cap {
	return []protocol.Cap{d.cap}
}

func (d *WsHeadLivenessCapDetector) DetectCaps(ctx context.Context) <-chan mapset.Set[protocol.Cap] {
	out := make(chan mapset.Set[protocol.Cap], 1)

	stateSub := subscribeStates(d.wsConn, d.name)
	if stateSub == nil || d.head == nil {
		// No ws connector or no head source: the cap can never be asserted.
		close(out)
		return out
	}
	headSub := d.head.Subscribe(fmt.Sprintf("%s_head", d.name))

	go func() {
		defer close(out)
		defer stateSub.Unsubscribe()
		defer headSub.Unsubscribe()

		tracker := &headLivenessTracker{
			now:               d.now,
			upstreamId:        d.upstreamId,
			measuredBlockTime: d.expectedBlockTime,
		}
		var wsConnected, headLive bool

		emit := func() bool {
			caps := mapset.NewThreadUnsafeSet[protocol.Cap]()
			if wsConnected && headLive {
				caps.Add(d.cap)
			}
			select {
			case out <- caps:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// The timer fires when the head produces no progress within tracker.timeout(); it is
		// re-armed with the current window at the bottom of every iteration.
		timer := time.NewTimer(tracker.timeout())
		defer timer.Stop()

		i := 0
		for {
			select {
			case <-ctx.Done():
				return
			case state, ok := <-stateSub.Events:
				if !ok {
					return
				}
				wsConnected = state == protocol.WsConnected
				if !emit() {
					return
				}
			case event, ok := <-headSub.Events:
				if !ok {
					return
				}
				i++
				fmt.Println(event.HeadData.Height, i)
				if i > 5 && i < 20 {
					continue
				}
				headLive = tracker.observe(event.HeadData.Height)
				if !emit() {
					return
				}
			case <-timer.C:
				headLive = tracker.onTimeout()
				if !emit() {
					return
				}
			}
			timer.Reset(tracker.timeout())
		}
	}()

	return out
}

var _ CapDetector = (*WsHeadLivenessCapDetector)(nil)
