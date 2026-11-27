package emerald_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type subscribeUpstreamStatusStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	// gate, when set, makes Send wait until it is closed
	gate      chan struct{}
	mu        sync.RWMutex
	responses []*dshackle.SubscribeUpstreamStatusResponse
	arrivals  []time.Time
}

func newSubscribeUpstreamStatusStream() *subscribeUpstreamStatusStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &subscribeUpstreamStatusStream{ctx: ctx, cancel: cancel}
}

func (s *subscribeUpstreamStatusStream) SetHeader(metadata.MD) error  { return nil }
func (s *subscribeUpstreamStatusStream) SendHeader(metadata.MD) error { return nil }
func (s *subscribeUpstreamStatusStream) SetTrailer(metadata.MD)       {}
func (s *subscribeUpstreamStatusStream) Context() context.Context     { return s.ctx }
func (s *subscribeUpstreamStatusStream) SendMsg(any) error            { return nil }
func (s *subscribeUpstreamStatusStream) RecvMsg(any) error            { return nil }

func (s *subscribeUpstreamStatusStream) Send(resp *dshackle.SubscribeUpstreamStatusResponse) error {
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
	s.mu.Lock()
	s.responses = append(s.responses, proto.Clone(resp).(*dshackle.SubscribeUpstreamStatusResponse))
	s.arrivals = append(s.arrivals, time.Now())
	s.mu.Unlock()
	return nil
}

func (s *subscribeUpstreamStatusStream) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.responses)
}

func (s *subscribeUpstreamStatusStream) ResponseAt(index int) *dshackle.SubscribeUpstreamStatusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return proto.Clone(s.responses[index]).(*dshackle.SubscribeUpstreamStatusResponse)
}

func (s *subscribeUpstreamStatusStream) ArrivalAt(index int) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.arrivals[index]
}

func (s *subscribeUpstreamStatusStream) Last() *dshackle.SubscribeUpstreamStatusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return proto.Clone(s.responses[len(s.responses)-1]).(*dshackle.SubscribeUpstreamStatusResponse)
}

// fakeUpstreamFeedSupervisor is a chain supervisor that also serves the
// per-upstream feed, with the views and the revision set by the test.
type fakeUpstreamFeedSupervisor struct {
	*fakeChainSupervisor

	feedMu        sync.Mutex
	views         map[string]upstreams.UpstreamView
	revision      uint64
	manager       *utils.SubscriptionManager[*upstreams.UpstreamStateEvent]
	subscriptions []*utils.Subscription[*upstreams.UpstreamStateEvent]
}

func newFakeUpstreamFeedSupervisor(chain chains.Chain) *fakeUpstreamFeedSupervisor {
	return &fakeUpstreamFeedSupervisor{
		fakeChainSupervisor: newFakeChainSupervisor(chain, upstreams.ChainSupervisorState{}),
		views:               make(map[string]upstreams.UpstreamView),
		manager:             utils.NewSubscriptionManager[*upstreams.UpstreamStateEvent]("fake-upstream-feed"),
	}
}

func (s *fakeUpstreamFeedSupervisor) SubscribeUpstreamStates(name string) *utils.Subscription[*upstreams.UpstreamStateEvent] {
	sub := s.manager.SubscribeWithSize(name, 1000)
	s.feedMu.Lock()
	s.subscriptions = append(s.subscriptions, sub)
	s.feedMu.Unlock()
	return sub
}

func (s *fakeUpstreamFeedSupervisor) GetUpstreamViews() upstreams.UpstreamViewsSnapshot {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	views := make(map[string]upstreams.UpstreamView, len(s.views))
	for id, view := range s.views {
		views[id] = view
	}
	return upstreams.UpstreamViewsSnapshot{Revision: s.revision, Views: views}
}

func (s *fakeUpstreamFeedSupervisor) GetUpstreamView(upstreamId string) (upstreams.UpstreamView, uint64, bool) {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	view, known := s.views[upstreamId]
	return view, s.revision, known
}

func (s *fakeUpstreamFeedSupervisor) SetView(upstreamId string, view upstreams.UpstreamView, revision uint64) {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	s.views[upstreamId] = view
	s.revision = revision
}

func (s *fakeUpstreamFeedSupervisor) DeleteView(upstreamId string, revision uint64) {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	delete(s.views, upstreamId)
	s.revision = revision
}

func (s *fakeUpstreamFeedSupervisor) Publish(event *upstreams.UpstreamStateEvent) {
	s.manager.Publish(event)
}

// OpenSubscriptions counts the feed subscriptions that were not unsubscribed.
// It drains what is queued on them, so call it only once the stream is over.
func (s *fakeUpstreamFeedSupervisor) OpenSubscriptions() int {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	open := 0
	for _, sub := range s.subscriptions {
		closed := false
		for drained := false; !drained && !closed; {
			select {
			case _, ok := <-sub.Events:
				closed = !ok
			default:
				drained = true
			}
		}
		if !closed {
			open++
		}
	}
	return open
}

func upstreamView(index string, head protocol.Block, supported ...string) upstreams.UpstreamView {
	state := protocol.DefaultUpstreamState(newMethodsMockWithSupported(supported...), mapset.NewThreadUnsafeSet[protocol.Cap](), index, nil, nil)
	state.HeadData = head
	return upstreams.UpstreamView{State: &state, SubMethods: mapset.NewThreadUnsafeSet[string]()}
}

func withStatus(view upstreams.UpstreamView, status protocol.AvailabilityStatus) upstreams.UpstreamView {
	state := *view.State
	state.Status = status
	return upstreams.UpstreamView{State: &state, SubMethods: view.SubMethods}
}

func upstreamStatusSupervisor(chainSupervisors ...upstreams.ChainSupervisor) (*mocks.UpstreamSupervisorMock, *utils.SubscriptionManager[upstreams.ChainSupervisorEvent]) {
	manager := utils.NewSubscriptionManager[upstreams.ChainSupervisorEvent]("chain-supervisors")
	upstreamSupervisor := mocks.NewUpstreamSupervisorMock()
	upstreamSupervisor.On("SubscribeChainSupervisor", mock.Anything).Return(manager.Subscribe("sub"))
	upstreamSupervisor.On("GetChainSupervisors").Return(chainSupervisors)
	return upstreamSupervisor, manager
}

// startUpstreamStatus runs the stream until the test ends and checks that it stops cleanly.
func startUpstreamStatus(t *testing.T, upstreamSupervisor upstreams.UpstreamSupervisor, stream *subscribeUpstreamStatusStream, minInterval, resyncInterval time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeUpstreamStatusWithResync(upstreamSupervisor, stream, minInterval, resyncInterval)
	}()
	t.Cleanup(func() {
		stream.cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Error("the stream did not stop after its context ended")
		}
	})
}

func waitForResponses(t *testing.T, stream *subscribeUpstreamStatusStream, count int) {
	t.Helper()
	require.Eventually(t, func() bool { return stream.Count() >= count }, 2*time.Second, 5*time.Millisecond,
		"expected %d responses, got %d", count, stream.Count())
}

func assertNoMoreResponses(t *testing.T, stream *subscribeUpstreamStatusStream, count int, wait time.Duration) {
	t.Helper()
	time.Sleep(wait)
	require.Equal(t, count, stream.Count(), "unexpected extra response")
}

func eventKinds(resp *dshackle.SubscribeUpstreamStatusResponse) []string {
	names := make([]string, 0)
	for _, event := range resp.GetChainDescription().GetChainEvent() {
		switch event.GetChainEvent().(type) {
		case *dshackle.ChainEvent_Status:
			names = append(names, "status")
		case *dshackle.ChainEvent_SupportedMethodsEvent:
			names = append(names, "methods")
		case *dshackle.ChainEvent_LowerBoundsEvent:
			names = append(names, "lowerBounds")
		case *dshackle.ChainEvent_Head:
			names = append(names, "head")
		case *dshackle.ChainEvent_FinalizationDataEvent:
			names = append(names, "blocks")
		case *dshackle.ChainEvent_SupportedSubscriptionsEvent:
			names = append(names, "subscriptions")
		case *dshackle.ChainEvent_NodesEvent:
			names = append(names, "nodes")
		default:
			names = append(names, fmt.Sprintf("%T", event.GetChainEvent()))
		}
	}
	return names
}

func headOf(resp *dshackle.SubscribeUpstreamStatusResponse) *dshackle.HeadEvent {
	for _, event := range resp.GetChainDescription().GetChainEvent() {
		if event.GetHead() != nil {
			return event.GetHead()
		}
	}
	return nil
}

func statusOf(resp *dshackle.SubscribeUpstreamStatusResponse) *dshackle.ChainStatus {
	for _, event := range resp.GetChainDescription().GetChainEvent() {
		if event.GetStatus() != nil {
			return event.GetStatus()
		}
	}
	return nil
}

var (
	fullKinds     = []string{"status", "methods", "lowerBounds", "head", "blocks", "subscriptions", "nodes"}
	metadataKinds = []string{"status", "methods", "lowerBounds", "blocks", "subscriptions", "nodes"}
)

const longResync = 10 * time.Second

func headChange(upstreamId, index string, revision uint64, head protocol.Block) *upstreams.UpstreamStateEvent {
	return &upstreams.UpstreamStateEvent{
		UpstreamId:    upstreamId,
		UpstreamIndex: index,
		Revision:      revision,
		Changes:       []upstreams.UpstreamStateWrapper{upstreams.NewHeadWrapper(head, upstreamId)},
	}
}

func statusChange(upstreamId, index string, revision uint64, status protocol.AvailabilityStatus) *upstreams.UpstreamStateEvent {
	return &upstreams.UpstreamStateEvent{
		UpstreamId:    upstreamId,
		UpstreamIndex: index,
		Revision:      revision,
		Changes:       []upstreams.UpstreamStateWrapper{upstreams.NewStatusWrapper(status)},
	}
}

func TestSubscribeUpstreamStatus_NilSupervisorReturnsError(t *testing.T) {
	err := emerald.SubscribeUpstreamStatus(nil, newSubscribeUpstreamStatusStream(), 0)

	require.Error(t, err)
}

func TestSubscribeUpstreamStatus_SendsFullForUpstreamWithHead(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()

	startUpstreamStatus(t, upstreamSupervisor, stream, 0, longResync)

	waitForResponses(t, stream, 1)
	full := stream.ResponseAt(0)
	assert.True(t, full.GetFullResponse())
	assert.NotNil(t, full.GetBuildInfo())
	assert.Equal(t, "up1", full.GetUpstreamId())
	assert.Equal(t, "00001", full.GetUpstreamIndex())
	assert.Equal(t, grpcId(chains.ARBITRUM), full.GetChainDescription().GetChain())
	assert.Equal(t, fullKinds, eventKinds(full))
	assert.Equal(t, uint64(123), headOf(full).GetHeight())
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_OK, statusOf(full).GetAvailability())
	assertNoMoreResponses(t, stream, 1, 50*time.Millisecond)
}

func TestSubscribeUpstreamStatus_WaitsForHead(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.Block{}, "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, stream, 0, longResync)
	assertNoMoreResponses(t, stream, 0, 100*time.Millisecond)

	// an event without a head for an upstream that still has none sends nothing
	feed.Publish(statusChange("up1", "00001", 6, protocol.Available))
	assertNoMoreResponses(t, stream, 0, 50*time.Millisecond)

	head := protocol.NewBlockWithHeight(124)
	feed.SetView("up1", upstreamView("00001", head, "eth_call"), 7)
	feed.Publish(headChange("up1", "00001", 7, head))

	waitForResponses(t, stream, 1)
	assert.True(t, stream.ResponseAt(0).GetFullResponse())
	assert.Equal(t, uint64(124), headOf(stream.ResponseAt(0)).GetHeight())
}

// The read that builds a first Full can be newer than the event that triggered
// it. The events still queued behind it must not bring older heads back.
func TestSubscribeUpstreamStatus_FirstFullComesFromReadAndOlderEventsAreIgnored(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.Block{}, "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, stream, 0, longResync)
	assertNoMoreResponses(t, stream, 0, 100*time.Millisecond)

	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(103), "eth_call"), 12)
	feed.Publish(headChange("up1", "00001", 10, protocol.NewBlockWithHeight(101)))
	feed.Publish(headChange("up1", "00001", 11, protocol.NewBlockWithHeight(102)))
	feed.Publish(headChange("up1", "00001", 12, protocol.NewBlockWithHeight(103)))

	waitForResponses(t, stream, 1)
	assert.True(t, stream.ResponseAt(0).GetFullResponse())
	assert.Equal(t, uint64(103), headOf(stream.ResponseAt(0)).GetHeight())
	assertNoMoreResponses(t, stream, 1, 100*time.Millisecond)
}

func TestSubscribeUpstreamStatus_SendsDeltaWithOnlyChangedEvents(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, stream, 0, longResync)
	waitForResponses(t, stream, 1)

	feed.Publish(statusChange("up1", "00001", 6, protocol.Unavailable))

	waitForResponses(t, stream, 2)
	delta := stream.ResponseAt(1)
	assert.False(t, delta.GetFullResponse())
	assert.Nil(t, delta.GetBuildInfo())
	assert.Equal(t, "up1", delta.GetUpstreamId())
	assert.Equal(t, "00001", delta.GetUpstreamIndex())
	assert.Equal(t, []string{"status"}, eventKinds(delta))
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE, statusOf(delta).GetAvailability())
}

func TestSubscribeUpstreamStatus_IgnoresEventAtOrBelowSnapshotRevision(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, stream, 0, longResync)
	waitForResponses(t, stream, 1)

	feed.Publish(headChange("up1", "00001", 4, protocol.NewBlockWithHeight(122)))
	feed.Publish(headChange("up1", "00001", 5, protocol.NewBlockWithHeight(123)))
	assertNoMoreResponses(t, stream, 1, 100*time.Millisecond)

	feed.Publish(headChange("up1", "00001", 6, protocol.NewBlockWithHeight(124)))
	waitForResponses(t, stream, 2)
	assert.Equal(t, uint64(124), headOf(stream.ResponseAt(1)).GetHeight())
}

func TestSubscribeUpstreamStatus_RemovalSendsUnavailableOnce(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, stream, 0, testResyncInterval)
	waitForResponses(t, stream, 1)

	feed.DeleteView("up1", 6)
	feed.Publish(&upstreams.UpstreamStateEvent{UpstreamId: "up1", UpstreamIndex: "00001", Revision: 6, Removed: true})

	// resyncs of the still-present upstream may already be queued ahead of it
	require.Eventually(t, func() bool {
		return stream.Count() >= 2 && eventKinds(stream.Last())[0] == "status" && len(eventKinds(stream.Last())) == 1
	}, 2*time.Second, 5*time.Millisecond)
	removal := stream.Last()
	assert.Equal(t, "up1", removal.GetUpstreamId())
	assert.Equal(t, "00001", removal.GetUpstreamIndex())
	assert.False(t, removal.GetFullResponse())
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE, statusOf(removal).GetAvailability())

	// the upstream is forgotten: later resyncs say nothing about it
	assertNoMoreResponses(t, stream, stream.Count(), 4*testResyncInterval)
}

func TestSubscribeUpstreamStatus_ResyncOmitsUnchangedHead(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()

	startUpstreamStatus(t, upstreamSupervisor, stream, 0, testResyncInterval)

	waitForResponses(t, stream, 3)
	for _, index := range []int{1, 2} {
		resync := stream.ResponseAt(index)
		assert.False(t, resync.GetFullResponse())
		assert.Nil(t, resync.GetBuildInfo())
		assert.Equal(t, "up1", resync.GetUpstreamId())
		assert.Equal(t, "00001", resync.GetUpstreamIndex())
		assert.Equal(t, metadataKinds, eventKinds(resync))
	}
}

// A head whose own event never arrived still reaches the client, once.
func TestSubscribeUpstreamStatus_ResyncIncludesHeadThatDiffers(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, stream, 0, testResyncInterval)
	waitForResponses(t, stream, 1)

	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(124), "eth_call"), 6)

	var withHead int
	require.Eventually(t, func() bool {
		for index := 1; index < stream.Count(); index++ {
			if headOf(stream.ResponseAt(index)) != nil {
				withHead = index
				return true
			}
		}
		return false
	}, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, fullKinds, eventKinds(stream.ResponseAt(withHead)))
	assert.False(t, stream.ResponseAt(withHead).GetFullResponse())
	assert.Equal(t, uint64(124), headOf(stream.ResponseAt(withHead)).GetHeight())

	waitForResponses(t, stream, withHead+3)
	assert.Equal(t, metadataKinds, eventKinds(stream.ResponseAt(withHead+1)))
	assert.Equal(t, metadataKinds, eventKinds(stream.ResponseAt(withHead+2)))
}

func TestSubscribeUpstreamStatus_ResyncRepairsLostRemoval(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, stream, 0, testResyncInterval)
	waitForResponses(t, stream, 1)

	feed.DeleteView("up1", 6)

	require.Eventually(t, func() bool {
		return stream.Count() >= 2 && len(eventKinds(stream.Last())) == 1
	}, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE, statusOf(stream.Last()).GetAvailability())
	assert.Equal(t, "00001", stream.Last().GetUpstreamIndex())
	assertNoMoreResponses(t, stream, stream.Count(), 4*testResyncInterval)
}

// The upstream was replaced under the same id and its removal event was lost.
func TestSubscribeUpstreamStatus_IndexChangeLeadsToFull(t *testing.T) {
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 5)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, stream, 0, longResync)
	waitForResponses(t, stream, 1)

	feed.SetView("up1", upstreamView("00007", protocol.NewBlockWithHeight(200), "eth_call"), 7)
	feed.Publish(statusChange("up1", "00007", 7, protocol.Available))

	waitForResponses(t, stream, 2)
	full := stream.ResponseAt(1)
	assert.True(t, full.GetFullResponse())
	assert.Equal(t, "00007", full.GetUpstreamIndex())
	assert.Equal(t, fullKinds, eventKinds(full))
	assert.Equal(t, uint64(200), headOf(full).GetHeight())
}

func TestSubscribeUpstreamStatus_FollowsAddedChainSupervisor(t *testing.T) {
	upstreamSupervisor, manager := upstreamStatusSupervisor()
	stream := newSubscribeUpstreamStatusStream()
	startUpstreamStatus(t, upstreamSupervisor, stream, 0, longResync)
	assertNoMoreResponses(t, stream, 0, 50*time.Millisecond)

	feed := newFakeUpstreamFeedSupervisor(chains.POLYGON)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(55), "eth_call"), 1)
	manager.Publish(&upstreams.AddChainSupervisorEvent{ChainSupervisor: feed})
	manager.Publish(&upstreams.AddChainSupervisorEvent{ChainSupervisor: feed})
	manager.Publish(&upstreams.AddChainSupervisorEvent{})

	waitForResponses(t, stream, 1)
	assert.Equal(t, grpcId(chains.POLYGON), stream.ResponseAt(0).GetChainDescription().GetChain())
	assertNoMoreResponses(t, stream, 1, 100*time.Millisecond)
}

func TestSubscribeUpstreamStatus_SkipsSupervisorWithoutFeed(t *testing.T) {
	plain := newFakeChainSupervisor(chains.ARBITRUM, newChainState(chains.ARBITRUM, protocol.NewBlockWithHeight(123), []string{"eth_call"}))
	feed := newFakeUpstreamFeedSupervisor(chains.POLYGON)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(55), "eth_call"), 1)
	upstreamSupervisor, _ := upstreamStatusSupervisor(plain, feed)
	stream := newSubscribeUpstreamStatusStream()

	startUpstreamStatus(t, upstreamSupervisor, stream, 0, longResync)

	waitForResponses(t, stream, 1)
	assert.Equal(t, grpcId(chains.POLYGON), stream.ResponseAt(0).GetChainDescription().GetChain())
	assertNoMoreResponses(t, stream, 1, 100*time.Millisecond)
}

func TestSubscribeUpstreamStatus_SkipsChainWithoutGrpcId(t *testing.T) {
	require.Zero(t, grpcId(chains.Unknown))
	unknown := newFakeUpstreamFeedSupervisor(chains.Unknown)
	unknown.SetView("up0", upstreamView("00000", protocol.NewBlockWithHeight(1), "eth_call"), 1)
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 1)
	upstreamSupervisor, _ := upstreamStatusSupervisor(unknown, feed)
	stream := newSubscribeUpstreamStatusStream()

	startUpstreamStatus(t, upstreamSupervisor, stream, 0, longResync)

	waitForResponses(t, stream, 1)
	assert.Equal(t, "up1", stream.ResponseAt(0).GetUpstreamId())
	assertNoMoreResponses(t, stream, 1, 100*time.Millisecond)
}

// A client that stops reading must not stall the supervisors, and once it reads
// again it must end up with the current state and never see an older head.
func TestSubscribeUpstreamStatus_SlowClientRecovers(t *testing.T) {
	const published = 2000
	feed := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	feed.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(1000), "eth_call"), 1)
	upstreamSupervisor, _ := upstreamStatusSupervisor(feed)
	stream := newSubscribeUpstreamStatusStream()
	stream.gate = make(chan struct{})
	startUpstreamStatus(t, upstreamSupervisor, stream, 0, testResyncInterval)

	publishing := make(chan struct{})
	go func() {
		defer close(publishing)
		for i := 1; i <= published; i++ {
			head := protocol.NewBlockWithHeight(uint64(1000 + i))
			feed.SetView("up1", upstreamView("00001", head, "eth_call"), uint64(1+i))
			feed.Publish(headChange("up1", "00001", uint64(1+i), head))
		}
		feed.SetView("up1", withStatus(upstreamView("00001", protocol.NewBlockWithHeight(1000+published), "eth_call"), protocol.Unavailable), published+2)
	}()
	select {
	case <-publishing:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing blocked: the stream stalls its feed")
	}

	close(stream.gate)

	var lastHead uint64
	var lastStatus dshackle.AvailabilityEnum
	require.Eventually(t, func() bool {
		lastHead, lastStatus = 0, dshackle.AvailabilityEnum_AVAIL_UNKNOWN
		for index := range stream.Count() {
			response := stream.ResponseAt(index)
			if head := headOf(response); head != nil {
				if head.GetHeight() < lastHead {
					t.Errorf("head went back from %d to %d", lastHead, head.GetHeight())
				}
				lastHead = head.GetHeight()
			}
			if status := statusOf(response); status != nil {
				lastStatus = status.GetAvailability()
			}
		}
		return lastHead == 1000+published && lastStatus == dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE
	}, 5*time.Second, 20*time.Millisecond)
}

func TestSubscribeUpstreamStatus_UnsubscribesWhenStreamEnds(t *testing.T) {
	arbitrum := newFakeUpstreamFeedSupervisor(chains.ARBITRUM)
	arbitrum.SetView("up1", upstreamView("00001", protocol.NewBlockWithHeight(123), "eth_call"), 1)
	polygon := newFakeUpstreamFeedSupervisor(chains.POLYGON)
	polygon.SetView("up2", upstreamView("00002", protocol.NewBlockWithHeight(55), "eth_call"), 1)
	upstreamSupervisor, _ := upstreamStatusSupervisor(arbitrum, polygon)
	stream := newSubscribeUpstreamStatusStream()
	done := make(chan error, 1)
	go func() {
		done <- emerald.SubscribeUpstreamStatusWithResync(upstreamSupervisor, stream, 0, longResync)
	}()
	waitForResponses(t, stream, 2)

	stream.cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not return after its context ended")
	}
	assert.Zero(t, arbitrum.OpenSubscriptions())
	assert.Zero(t, polygon.OpenSubscriptions())
}
