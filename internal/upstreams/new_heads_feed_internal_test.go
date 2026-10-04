package upstreams

import (
	"fmt"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/blockchain"
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
	return &feedHarness{t: t, fc: choice.NewHeightForkChoice(), feed: &newHeadsFeed{chain: t.Name()}}
}

func (h *feedHarness) send(upstreamId string, status protocol.AvailabilityStatus, block protocol.Block) (uint64, bool) {
	event := &protocol.HeadUpstreamEvent{Status: status, Head: block}
	updated, head := h.fc.Choose(upstreamId, event)
	announced, ok := h.feed.onHead(event, updated, head)
	return announced.Height, ok
}

// ws is a subscription head, poll a polled one; fork tells blocks of the same height apart.
func (h *feedHarness) ws(upstreamId string, height uint64, fork int) (uint64, bool) {
	block := testBlock(height, fork)
	block.RawData = []byte(fmt.Sprintf(`{"number":"%#x"}`, height))
	return h.send(upstreamId, protocol.Available, block)
}

func (h *feedHarness) poll(upstreamId string, height uint64, fork int) (uint64, bool) {
	return h.send(upstreamId, protocol.Available, testBlock(height, fork))
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

func skipped(t *testing.T) float64 {
	var m dto.Metric
	assert.NoError(t, newHeadsSkippedMetric.WithLabelValues(t.Name()).Write(&m))
	return m.GetCounter().GetValue()
}

func TestNewHeadsFeedAnnouncesSubscriptionWinner(t *testing.T) {
	h := newFeedHarness(t)

	announcedAt(t, 100)(h.ws("ws", 100, 0))
	notAnnounced(t)(h.poll("poll", 100, 0))
	announcedAt(t, 101)(h.ws("ws", 101, 0))
}

func TestNewHeadsFeedAnnouncesSubscriptionHeadAfterPolledOne(t *testing.T) {
	h := newFeedHarness(t)

	notAnnounced(t)(h.poll("poll", 100, 0))
	announcedAt(t, 100)(h.ws("ws", 100, 0))
	notAnnounced(t)(h.poll("poll", 101, 0))
	announcedAt(t, 101)(h.ws("ws", 101, 0))
}

func TestNewHeadsFeedCatchesUpWhilePolledHeadIsAhead(t *testing.T) {
	h := newFeedHarness(t)

	notAnnounced(t)(h.poll("poll", 100, 0))
	notAnnounced(t)(h.poll("poll", 103, 0)) // the chain head jumps
	announcedAt(t, 100)(h.ws("ws", 100, 0))
	announcedAt(t, 101)(h.ws("ws", 101, 0))
	announcedAt(t, 102)(h.ws("ws", 102, 0))
	announcedAt(t, 103)(h.ws("ws", 103, 0))
	announcedAt(t, 104)(h.ws("ws", 104, 0)) // the subscription head overtakes and wins
	assert.Zero(t, skipped(t))
}

func TestNewHeadsFeedAnnouncesEachHeightOnceInOrder(t *testing.T) {
	h := newFeedHarness(t)

	announcedAt(t, 100)(h.ws("ws-a", 100, 0))
	notAnnounced(t)(h.ws("ws-b", 100, 0))
	notAnnounced(t)(h.poll("poll", 102, 0))
	announcedAt(t, 102)(h.ws("ws-a", 102, 0))
	notAnnounced(t)(h.ws("ws-b", 101, 0)) // older than what subscribers already got
	notAnnounced(t)(h.ws("ws-b", 102, 0))
}

func TestNewHeadsFeedIgnoresLaggingAndUnavailableHeads(t *testing.T) {
	h := newFeedHarness(t)

	notAnnounced(t)(h.poll("poll", 1000, 0))
	notAnnounced(t)(h.ws("ws-lagging", 1000-newHeadsLagWindow, 0))
	block := testBlock(1000, 0)
	block.RawData = []byte(`{}`)
	notAnnounced(t)(h.send("ws-syncing", protocol.Syncing, block))
	announcedAt(t, 1000-newHeadsLagWindow+1)(h.ws("ws-lagging", 1000-newHeadsLagWindow+1, 0))
}

func TestNewHeadsFeedAnnouncesReorgedHead(t *testing.T) {
	h := newFeedHarness(t)

	announcedAt(t, 100)(h.ws("ws", 100, 0))
	announcedAt(t, 101)(h.ws("ws", 101, 0))
	announcedAt(t, 100)(h.ws("ws", 100, 1)) // the chain head moves down to another block
	announcedAt(t, 101)(h.ws("ws", 101, 1))
}

func TestNewHeadsFeedDoesNotRepeatWhenUpstreamLeaves(t *testing.T) {
	h := newFeedHarness(t)

	announcedAt(t, 100)(h.ws("ws-slow", 100, 0))
	announcedAt(t, 101)(h.ws("ws-fast", 101, 0))
	announcedAt(t, 102)(h.ws("ws-fast", 102, 0))
	// the fast upstream leaves: the chain head falls back to 100, subscribers already got 101-102
	notAnnounced(t)(h.send("ws-fast", protocol.Unavailable, testBlock(102, 0)))
	notAnnounced(t)(h.ws("ws-slow", 101, 0))
	notAnnounced(t)(h.ws("ws-slow", 102, 0))
	announcedAt(t, 103)(h.ws("ws-slow", 103, 0))
}

func TestNewHeadsFeedCountsSkippedHeights(t *testing.T) {
	h := newFeedHarness(t)

	announcedAt(t, 100)(h.ws("ws", 100, 0))
	for height := uint64(101); height <= 110; height++ {
		notAnnounced(t)(h.poll("poll", height, 0))
	}
	// the subscription upstream comes back at the tip: 101-110 are gone for good
	announcedAt(t, 111)(h.ws("ws", 111, 0))
	assert.Equal(t, 10.0, skipped(t))
}
