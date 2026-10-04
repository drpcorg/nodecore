package upstreams

import (
	"fmt"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
)

// feedHarness drives the feed the way updateHead does: fork choice first, then the feed.
type feedHarness struct {
	t    *testing.T
	fc   *choice.HeightForkChoice
	feed *newHeadsFeed
}

func newFeedHarness(t *testing.T) *feedHarness {
	return &feedHarness{t: t, fc: choice.NewHeightForkChoice(), feed: newNewHeadsFeed(t.Name())}
}

func (h *feedHarness) send(upstreamId string, status protocol.AvailabilityStatus, block protocol.Block) (uint64, bool) {
	event := &protocol.HeadUpstreamEvent{Status: status, Head: block}
	updated, head := h.fc.Choose(upstreamId, event)
	announced, ok := h.feed.onHead(event, updated, head)
	if ok {
		assert.NotEmpty(h.t, announced.RawData)
	}
	return announced.Height, ok
}

// ws is a subscription head, poll a polled one; fork tells blocks of the same height apart.
func (h *feedHarness) ws(upstreamId string, height uint64, fork int) (uint64, bool) {
	block := testBlock(height, fork)
	block.RawData = []byte(fmt.Sprintf(`{"number":"%#x","hash":"%s"}`, height, block.Hash.ToHex()))
	return h.send(upstreamId, protocol.Available, block)
}

func (h *feedHarness) poll(upstreamId string, height uint64, fork int) (uint64, bool) {
	return h.send(upstreamId, protocol.Available, testBlock(height, fork))
}

func counterValue(t *testing.T, counter *prometheus.CounterVec) float64 {
	var m dto.Metric
	assert.NoError(t, counter.WithLabelValues(t.Name()).Write(&m))
	return m.GetCounter().GetValue()
}

func testBlock(height uint64, fork int) protocol.Block {
	return protocol.NewBlock(
		height, 0,
		blockchain.NewHashIdFromString(fmt.Sprintf("%016x%04x", height, fork)),
		blockchain.NewHashIdFromString(fmt.Sprintf("%016x%04x", height-1, fork)),
	)
}

func announcedAt(t *testing.T, height uint64) func(uint64, bool) {
	return func(got uint64, ok bool) {
		t.Helper()
		assert.True(t, ok, "expected height %d to be announced", height)
		assert.Equal(t, height, got)
	}
}

func notAnnounced(t *testing.T) func(uint64, bool) {
	return func(got uint64, ok bool) {
		t.Helper()
		assert.False(t, ok, "unexpected announcement of height %d", got)
	}
}

func TestNewHeadsFeedAnnouncesSubscriptionWinner(t *testing.T) {
	h := newFeedHarness(t)

	announcedAt(t, 100)(h.ws("ws", 100, 0))
	notAnnounced(t)(h.poll("poll", 100, 0))
	announcedAt(t, 101)(h.ws("ws", 101, 0))
	assert.Zero(t, counterValue(t, newHeadsLateMetric))
}

func TestNewHeadsFeedAnnouncesLateSubscriptionHead(t *testing.T) {
	h := newFeedHarness(t)

	notAnnounced(t)(h.poll("poll", 100, 0))
	announcedAt(t, 100)(h.ws("ws", 100, 0))
	notAnnounced(t)(h.poll("poll", 101, 0))
	announcedAt(t, 101)(h.ws("ws", 101, 0))
	assert.Equal(t, 2.0, counterValue(t, newHeadsLateMetric))
}

func TestNewHeadsFeedCatchesUpWhilePolledHeadIsAhead(t *testing.T) {
	h := newFeedHarness(t)

	notAnnounced(t)(h.poll("poll", 100, 0))
	notAnnounced(t)(h.poll("poll", 101, 0))
	notAnnounced(t)(h.poll("poll", 102, 0))
	announcedAt(t, 100)(h.ws("ws", 100, 0))
	announcedAt(t, 101)(h.ws("ws", 101, 0))
	announcedAt(t, 102)(h.ws("ws", 102, 0))
	announcedAt(t, 103)(h.ws("ws", 103, 0)) // the subscription head overtakes and wins
}

func TestNewHeadsFeedFillsHeightsTheChainHeadJumpedOver(t *testing.T) {
	h := newFeedHarness(t)

	notAnnounced(t)(h.poll("poll", 100, 0))
	announcedAt(t, 100)(h.ws("ws", 100, 0))
	notAnnounced(t)(h.poll("poll", 103, 0))
	announcedAt(t, 101)(h.ws("ws", 101, 0))
	announcedAt(t, 102)(h.ws("ws", 102, 0))
	announcedAt(t, 103)(h.ws("ws", 103, 0))
	assert.Zero(t, counterValue(t, newHeadsSkippedMetric))
}

func TestNewHeadsFeedAnnouncesEachHeightOnce(t *testing.T) {
	h := newFeedHarness(t)

	announcedAt(t, 100)(h.ws("ws-a", 100, 0))
	notAnnounced(t)(h.ws("ws-b", 100, 0))
	notAnnounced(t)(h.poll("poll", 101, 0))
	announcedAt(t, 101)(h.ws("ws-b", 101, 0))
	notAnnounced(t)(h.ws("ws-a", 101, 0))
}

func TestNewHeadsFeedSkipsBlocksOffTheChainHead(t *testing.T) {
	h := newFeedHarness(t)

	announcedAt(t, 100)(h.ws("ws", 100, 0))
	notAnnounced(t)(h.poll("poll", 101, 0))
	notAnnounced(t)(h.ws("ws", 101, 1)) // another block at the chain head height
	announcedAt(t, 102)(h.ws("ws", 102, 1))
	assert.Equal(t, 1.0, counterValue(t, newHeadsSkippedMetric))
}

func TestNewHeadsFeedKeepsHeightsInOrder(t *testing.T) {
	h := newFeedHarness(t)

	notAnnounced(t)(h.poll("poll", 100, 0))
	notAnnounced(t)(h.poll("poll", 101, 0))
	announcedAt(t, 101)(h.ws("ws-fast", 101, 0))
	notAnnounced(t)(h.ws("ws-slow", 100, 0))               // older than what subscribers already got
	assert.Zero(t, counterValue(t, newHeadsSkippedMetric)) // nothing announced before 101
}

func TestNewHeadsFeedIgnoresLaggingAndUnavailableHeads(t *testing.T) {
	h := newFeedHarness(t)

	notAnnounced(t)(h.poll("poll", 100, 0))
	notAnnounced(t)(h.poll("poll", 1000, 0))
	notAnnounced(t)(h.ws("ws-lagging", 500, 0)) // jumped over, but outside the window
	block := testBlock(1000, 0)
	block.RawData = []byte(`{}`)
	notAnnounced(t)(h.send("ws-syncing", protocol.Syncing, block))
	announcedAt(t, 1000)(h.ws("ws", 1000, 0))
}

func TestNewHeadsFeedAnnouncesReorgedHead(t *testing.T) {
	h := newFeedHarness(t)

	announcedAt(t, 100)(h.ws("ws", 100, 0))
	announcedAt(t, 101)(h.ws("ws", 101, 0))
	announcedAt(t, 100)(h.ws("ws", 100, 1)) // the chain head moves down to another block
	announcedAt(t, 101)(h.ws("ws", 101, 1))
}

func TestNewHeadsFeedAnnouncesLateReorgedHead(t *testing.T) {
	h := newFeedHarness(t)

	notAnnounced(t)(h.poll("poll", 101, 0))
	announcedAt(t, 101)(h.ws("ws", 101, 0))
	notAnnounced(t)(h.poll("poll", 100, 1))
	// both upstreams moved to the other fork; whichever the fork choice keeps, subscribers get it
	announcedAt(t, 100)(h.ws("ws", 100, 1))
	notAnnounced(t)(h.poll("poll", 101, 1))
	announcedAt(t, 101)(h.ws("ws", 101, 1))
}

func TestNewHeadsFeedDoesNotRepeatAfterUpstreamRemoval(t *testing.T) {
	h := newFeedHarness(t)

	notAnnounced(t)(h.poll("poll", 100, 0))
	announcedAt(t, 100)(h.ws("ws", 100, 0))
	notAnnounced(t)(h.poll("poll", 101, 0))
	// the polled upstream leaves: the chain head falls back to the already announced 100
	notAnnounced(t)(h.send("poll", protocol.Unavailable, testBlock(101, 0)))
	announcedAt(t, 101)(h.ws("ws", 101, 0))
}

func TestNewHeadsFeedCountsSkippedHeights(t *testing.T) {
	h := newFeedHarness(t)

	announcedAt(t, 100)(h.ws("ws", 100, 0))
	for height := uint64(101); height <= 110; height++ {
		notAnnounced(t)(h.poll("poll", height, 0))
	}
	// the subscription upstream reconnects at the tip, the gap is gone for good
	announcedAt(t, 111)(h.ws("ws", 111, 0))
	notAnnounced(t)(h.ws("ws", 105, 0))
	assert.Equal(t, 10.0, counterValue(t, newHeadsSkippedMetric))
}

func TestNewHeadsFeedPrunesOldHeights(t *testing.T) {
	h := newFeedHarness(t)

	for height := uint64(1); height <= 1000; height++ {
		h.poll("poll", height, 0)
		h.ws("ws", height, 0)
	}
	assert.LessOrEqual(t, len(h.feed.chainHeads), newHeadsFeedWindow)
	assert.LessOrEqual(t, len(h.feed.announced), newHeadsFeedWindow)
}
