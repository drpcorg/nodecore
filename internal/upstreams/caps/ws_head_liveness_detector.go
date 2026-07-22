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
	// checkedBlocksUntilLive is how many blocks in a row must be observed before a head
	// is considered live. N blocks == N-1 consecutive height increments.
	checkedBlocksUntilLive = 10
	// maxWarmup caps how long the consecutive run must last before going live, so slow
	// chains don't wait for the full block count. A head goes live once it has produced
	// checkedBlocksUntilLive consecutive blocks OR maxWarmup has elapsed since the run
	// started, whichever comes first.
	maxWarmup = time.Minute
)

// HeadSource is the subset of blocks.HeadProcessor the detector consumes: a stream of new
// heads. blocks.HeadProcessor satisfies it.
type HeadSource interface {
	Subscribe(name string) *utils.Subscription[blocks.HeadEvent]
}

// headLivenessTracker reduces a stream of head heights into a "live" verdict: the head is
// live once its current consecutive run reaches either checkedBlocksUntilLive blocks or
// maxWarmup of elapsed time, whichever comes first. A gap or a backward/reorg resets the
// run. Duplicate heights are neutral. There is no stall/recency timeout - a head that
// stops producing simply freezes the last verdict.
type headLivenessTracker struct {
	upstreamId string
	lastHeight uint64
	haveLast   bool
	count      int
	live       bool
	// lastNonConsecutiveTime is the timestamp of the last non-consecutive event, or the
	// first observation if none has happened yet (i.e. the start of the current run).
	lastNonConsecutiveTime time.Time
	now                    func() time.Time
}

func (h *headLivenessTracker) observe(height uint64) bool {
	now := h.now()
	if !h.haveLast {
		h.haveLast = true
		h.lastHeight = height
		h.lastNonConsecutiveTime = now // baseline; don't leave zero-valued
		return h.live
	}
	diff := int64(height) - int64(h.lastHeight)
	switch diff {
	case 0:
		// duplicate height (e.g. the initial getLatestBlock vs the first sub event) -
		// neutral: neither progress nor a gap.
	case 1:
		h.count++
		if h.count >= checkedBlocksUntilLive-1 || now.Sub(h.lastNonConsecutiveTime) >= maxWarmup {
			h.live = true
		}
	default:
		// a gap (diff >= 2) or a backward/reorg (diff < 0): the head is not advancing
		// consecutively - reset the run.
		if h.live {
			log.
				Warn().
				Msgf(
					"non consecutive blocks in subscription head of upstream '%s', last height - %d, current height - %d, diff - %d",
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

// WsHeadLivenessCapDetector asserts a single cap while the given websocket connector is
// connected AND its head is advancing consecutively, and retracts it otherwise. It is
// used to gate WsCap on ws-driven EVM heads: a flapping head (missed blocks / reorgs)
// pulls the upstream out of subscription serving without affecting regular RPC routing.
// If the connector isn't a websocket (SubscribeStates returns nil) or there is no head
// source, the cap is never asserted.
type WsHeadLivenessCapDetector struct {
	upstreamId string
	name       string
	cap        protocol.Cap
	wsConn     connectors.ApiConnector
	head       HeadSource
	now        func() time.Time
}

func NewWsHeadLivenessCapDetector(upstreamId, name string, cap protocol.Cap, wsConn connectors.ApiConnector, head HeadSource) *WsHeadLivenessCapDetector {
	return &WsHeadLivenessCapDetector{
		upstreamId: upstreamId,
		name:       name,
		cap:        cap,
		wsConn:     wsConn,
		head:       head,
		now:        time.Now,
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

		tracker := &headLivenessTracker{now: d.now, upstreamId: d.upstreamId}
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
				headLive = tracker.observe(event.HeadData.Height)
				if !emit() {
					return
				}
			}
		}
	}()

	return out
}

var _ CapDetector = (*WsHeadLivenessCapDetector)(nil)
