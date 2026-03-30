package upstreams_test

import (
	"context"
	"sync"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var loadMethodSpecsOnce sync.Once

func TestBaseUpstreamStart_WithoutProcessors_PublishesAvailableState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available

	assert.Equal(t, "id", upstream.GetId())
	assert.Equal(t, chains.ETHEREUM, upstream.GetChain())
	assert.Equal(t, "00012", upstream.GetHashIndex())
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())

	emit(&protocol.StatusUpstreamStateEvent{Status: protocol.Unavailable})
	event := nextUpstreamEvent(t, sub)
	expectedState.Status = protocol.Unavailable
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamStop_StopsRunningLifecycle(t *testing.T) {
	upstream, _, _ := newTestBaseUpstream(t, nil, nil, nil)

	upstream.Start()
	require.True(t, upstream.Running())

	upstream.Stop()

	assert.False(t, upstream.Running())
}

func TestBaseUpstreamProcessStateEvents_UpdatesHeadState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	headData := protocol.NewBlockWithHeight(123)
	emit(&protocol.HeadUpstreamStateEvent{HeadData: headData})

	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	expectedState.HeadData = headData
	assertHeadEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_UpdatesBlockState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	blockData := protocol.NewBlockWithHeight(456)
	emit(&protocol.BlockUpstreamStateEvent{Block: blockData, BlockType: protocol.FinalizedBlock})

	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	expectedState.BlockInfo.AddBlock(blockData, protocol.FinalizedBlock)
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_IgnoresDuplicateBlockState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	blockData := protocol.NewBlockWithHeight(456)
	blockEvent := &protocol.BlockUpstreamStateEvent{Block: blockData, BlockType: protocol.FinalizedBlock}

	emit(blockEvent)
	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	expectedState.BlockInfo.AddBlock(blockData, protocol.FinalizedBlock)
	assertStateEventMatches(t, event, expectedState)

	emit(blockEvent)
	assertNoUpstreamEvent(t, sub)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_UpdatesLowerBoundsState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	bound := protocol.LowerBoundData{Type: protocol.SlotBound, Bound: 789, Timestamp: time.Now().Unix()}
	emit(&protocol.LowerBoundUpstreamStateEvent{Data: bound})

	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	expectedState.LowerBoundsInfo.AddLowerBound(bound)
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_IgnoresDuplicateLowerBoundsState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	bound := protocol.LowerBoundData{Type: protocol.SlotBound, Bound: 789, Timestamp: time.Now().Unix()}
	boundEvent := &protocol.LowerBoundUpstreamStateEvent{Data: bound}

	emit(boundEvent)
	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	expectedState.LowerBoundsInfo.AddLowerBound(bound)
	assertStateEventMatches(t, event, expectedState)

	emit(boundEvent)
	assertNoUpstreamEvent(t, sub)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_UpdatesLabelsState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	emit(&protocol.LabelsUpstreamStateEvent{Labels: lo.T2("region", "us-east-1")})

	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	expectedState.Labels.AddLabel("region", "us-east-1")
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_IgnoresDuplicateStatusState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	emit(&protocol.StatusUpstreamStateEvent{Status: protocol.Available})

	assertNoUpstreamEvent(t, sub)

	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_IgnoresDuplicateLabelsState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	labelEvent := &protocol.LabelsUpstreamStateEvent{Labels: lo.T2("region", "us-east-1")}

	emit(labelEvent)
	event := nextUpstreamEvent(t, sub)

	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	expectedState.Labels.AddLabel("region", "us-east-1")
	assertStateEventMatches(t, event, expectedState)

	emit(labelEvent)
	assertNoUpstreamEvent(t, sub)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_DuplicateHeadStateStillPublishes(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	headData := protocol.NewBlockWithHeight(123)
	headEvent := &protocol.HeadUpstreamStateEvent{HeadData: headData}

	emit(headEvent)
	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	expectedState.HeadData = headData
	assertHeadEventMatches(t, event, expectedState)

	emit(headEvent)
	event = nextUpstreamEvent(t, sub)
	assertHeadEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_AddsWsCapOnConnected(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	emit(&protocol.SubscribeUpstreamStateEvent{State: protocol.WsConnected})

	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_IgnoresDuplicateWsConnectedState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	wsConnectedEvent := &protocol.SubscribeUpstreamStateEvent{State: protocol.WsConnected}

	emit(wsConnectedEvent)
	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	assertStateEventMatches(t, event, expectedState)

	emit(wsConnectedEvent)
	assertNoUpstreamEvent(t, sub)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_RemovesWsCapOnDisconnected(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	emit(&protocol.SubscribeUpstreamStateEvent{State: protocol.WsConnected})
	_ = nextUpstreamEvent(t, sub)

	emit(&protocol.SubscribeUpstreamStateEvent{State: protocol.WsDisconnected})

	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_IgnoresDuplicateWsDisconnectedState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	emit(&protocol.SubscribeUpstreamStateEvent{State: protocol.WsConnected})
	_ = nextUpstreamEvent(t, sub)

	wsDisconnectedEvent := &protocol.SubscribeUpstreamStateEvent{State: protocol.WsDisconnected}
	emit(wsDisconnectedEvent)
	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	assertStateEventMatches(t, event, expectedState)

	emit(wsDisconnectedEvent)
	assertNoUpstreamEvent(t, sub)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_FatalErrorSuppressesStateUntilValid(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	emit(&protocol.FatalErrorUpstreamStateEvent{})
	event := nextUpstreamEvent(t, sub)
	_, ok := event.EventType.(*protocol.RemoveUpstreamEvent)
	require.True(t, ok)

	emit(&protocol.StatusUpstreamStateEvent{Status: protocol.Unavailable})
	assertNoUpstreamEvent(t, sub)
	assert.Equal(t, protocol.Available, upstream.GetUpstreamState().Status)

	emit(&protocol.ValidUpstreamStateEvent{})
	event = nextUpstreamEvent(t, sub)
	_, ok = event.EventType.(*protocol.ValidUpstreamEvent)
	require.True(t, ok)

	emit(&protocol.StatusUpstreamStateEvent{Status: protocol.Unavailable})
	event = nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Unavailable
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_IgnoresDuplicateFatalErrorState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	emit(&protocol.FatalErrorUpstreamStateEvent{})
	event := nextUpstreamEvent(t, sub)
	_, ok := event.EventType.(*protocol.RemoveUpstreamEvent)
	require.True(t, ok)

	emit(&protocol.FatalErrorUpstreamStateEvent{})
	assertNoUpstreamEvent(t, sub)

	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_IgnoresDuplicateValidState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	emit(&protocol.ValidUpstreamStateEvent{})
	assertNoUpstreamEvent(t, sub)

	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_IgnoresDuplicateValidStateAfterRecovery(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	emit(&protocol.FatalErrorUpstreamStateEvent{})
	event := nextUpstreamEvent(t, sub)
	_, ok := event.EventType.(*protocol.RemoveUpstreamEvent)
	require.True(t, ok)

	emit(&protocol.ValidUpstreamStateEvent{})
	event = nextUpstreamEvent(t, sub)
	_, ok = event.EventType.(*protocol.ValidUpstreamEvent)
	require.True(t, ok)

	emit(&protocol.ValidUpstreamStateEvent{})
	assertNoUpstreamEvent(t, sub)

	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamBanMethod_BansAndUnbansMethod(t *testing.T) {
	loadMethodSpecs(t)

	upConfig := newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
	upstream, _, sub := newTestBaseUpstream(t, upConfig, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()
	expectedInitialState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, upConfig.Methods),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedInitialState.Status = protocol.Available
	assertUpstreamStateMatches(t, expectedInitialState, upstream.GetUpstreamState())

	upstream.BanMethod("eth_call")

	event := nextUpstreamEvent(t, sub)
	expectedBannedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, &config.MethodsConfig{
			BanDuration:    upConfig.Methods.BanDuration,
			EnableMethods:  upConfig.Methods.EnableMethods,
			DisableMethods: []string{"eth_call"},
		}),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedBannedState.Status = protocol.Available
	assertStateEventMatches(t, event, expectedBannedState)
	assertUpstreamStateMatches(t, expectedBannedState, upstream.GetUpstreamState())

	event = nextUpstreamEvent(t, sub)
	assertStateEventMatches(t, event, expectedInitialState)
	assertUpstreamStateMatches(t, expectedInitialState, upstream.GetUpstreamState())
}

func TestBaseUpstreamBanMethod_IgnoresEnabledMethod(t *testing.T) {
	loadMethodSpecs(t)

	upConfig := newUpstreamConfig(&config.MethodsConfig{
		BanDuration:   20 * time.Millisecond,
		EnableMethods: []string{"eth_call"},
	})
	upstream, _, sub := newTestBaseUpstream(t, upConfig, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	upstream.BanMethod("eth_call")

	assertNoUpstreamEvent(t, sub)
	assert.True(t, upstream.GetUpstreamState().UpstreamMethods.HasMethod("eth_call"))
}

func TestBaseUpstreamGetConnector_ReturnsMatchingConnector(t *testing.T) {
	httpConnector := mocks.NewConnectorMockWithType(protocol.JsonRpcConnector)
	wsConnector := mocks.NewConnectorMockWithType(protocol.WsConnector)

	upstream, _, _ := newTestBaseUpstream(t, nil, []*mocks.ConnectorMock{httpConnector, wsConnector}, nil)

	assert.Same(t, httpConnector, upstream.GetConnector(protocol.JsonRpcConnector))
	assert.Same(t, wsConnector, upstream.GetConnector(protocol.WsConnector))
	assert.Nil(t, upstream.GetConnector(protocol.RestConnector))
}

func TestBaseUpstreamUpdateHead_DelegatesToHeadProcessor(t *testing.T) {
	headProcessor := mocks.NewHeadProcessorMock()
	headProcessor.On("UpdateHead", uint64(100), uint64(7)).Once()

	headEventProcessor := event_processors.NewHeadEventProcessor(context.Background(), "id", chains.ETHEREUM, headProcessor)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{headEventProcessor})
	upstream, _, _ := newTestBaseUpstream(t, nil, nil, aggregator)

	upstream.UpdateHead(100, 7)

	headProcessor.AssertExpectations(t)
}

func TestBaseUpstreamUpdateHead_DelegatesToBlockProcessor(t *testing.T) {
	blockProcessor := mocks.NewBlockProcessorMock()
	blockData := protocol.NewBlock(uint64(1002), 0, blockchain.EmptyHash, blockchain.EmptyHash)
	blockProcessor.On("UpdateBlock", blockData, protocol.FinalizedBlock).Once()

	blockEventProcessor := event_processors.NewBaseBlockEventProcessor(context.Background(), "id", chains.ETHEREUM, blockProcessor)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{blockEventProcessor})
	upstream, _, _ := newTestBaseUpstream(t, nil, nil, aggregator)

	upstream.UpdateBlock(blockData, protocol.FinalizedBlock)

	blockProcessor.AssertExpectations(t)
}

func TestBaseUpstreamStart_WithFatalSettingsValidation_DoesNotRun(t *testing.T) {
	validator := mocks.NewSettingsValidatorMock()
	validator.On("Validate").Return(validations.FatalSettingError).Once()

	upConfig := newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
	settingsProcessor := event_processors.NewBaseSettingsEventProcessor(
		context.Background(),
		"id",
		testUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator}),
	)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{settingsProcessor})
	upstream, _, _ := newTestBaseUpstream(t, upConfig, nil, aggregator)

	upstream.Start()

	assert.False(t, upstream.Running())
	validator.AssertExpectations(t)
}

func TestBaseUpstreamStart_WithSettingsError_KeepsRunningWithoutPublishingState(t *testing.T) {
	validator := mocks.NewSettingsValidatorMock()
	validator.On("Validate").Return(validations.SettingsError)

	settingsProcessor := event_processors.NewBaseSettingsEventProcessor(
		context.Background(),
		"id",
		testUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator}),
	)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{settingsProcessor})
	upstream, _, sub := newTestBaseUpstream(t, nil, nil, aggregator)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	assert.True(t, upstream.Running())
	assertNoUpstreamEvent(t, sub)
}

func newTestBaseUpstream(
	t *testing.T,
	upConfig *config.Upstream,
	connectorMocks []*mocks.ConnectorMock,
	aggregator *event_processors.UpstreamProcessorAggregator,
) (*upstreams.BaseUpstream, func(protocol.AbstractUpstreamStateEvent), *utils.Subscription[protocol.UpstreamEvent]) {
	t.Helper()
	loadMethodSpecs(t)

	if upConfig == nil {
		upConfig = newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
	}

	upstreamMethods, err := methods.NewUpstreamMethods("eth", upConfig.Methods)
	require.NoError(t, err)

	state := utils.NewAtomic[protocol.UpstreamState]()
	state.Store(protocol.DefaultUpstreamState(
		upstreamMethods,
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	))

	stateChan := make(chan protocol.AbstractUpstreamStateEvent, 100)
	emitter := func(event protocol.AbstractUpstreamStateEvent) {
		stateChan <- event
	}
	var stateEmitter event_processors.Emitter = emitter

	apiConnectors := make([]connectors.ApiConnector, 0, len(connectorMocks))
	for _, connector := range connectorMocks {
		apiConnectors = append(apiConnectors, connector)
	}

	upstream := upstreams.NewBaseUpstreamWithParams(
		"id",
		chains.ETHEREUM,
		apiConnectors,
		upConfig,
		"00012",
		state,
		aggregator,
		&stateChan,
		&stateEmitter,
	)

	sub := upstream.Subscribe(t.Name())

	return upstream, emitter, sub
}

func newUpstreamConfig(methodsConfig *config.MethodsConfig) *config.Upstream {
	if methodsConfig == nil {
		methodsConfig = &config.MethodsConfig{BanDuration: 20 * time.Millisecond}
	}

	return &config.Upstream{
		Id:      "id",
		Methods: methodsConfig,
		Options: testUpstreamOptions(),
	}
}

func nextUpstreamEvent(t *testing.T, sub *utils.Subscription[protocol.UpstreamEvent]) protocol.UpstreamEvent {
	t.Helper()

	select {
	case event := <-sub.Events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for upstream event")
		return protocol.UpstreamEvent{}
	}
}

func assertStateEventMatches(t *testing.T, event protocol.UpstreamEvent, expected protocol.UpstreamState) {
	t.Helper()

	stateEvent, ok := event.EventType.(*protocol.StateUpstreamEvent)
	require.True(t, ok)
	assertUpstreamStateMatches(t, expected, *stateEvent.State)
}

func assertHeadEventMatches(t *testing.T, event protocol.UpstreamEvent, expected protocol.UpstreamState) {
	t.Helper()

	headEvent, ok := event.EventType.(*protocol.HeadUpstreamEvent)
	require.True(t, ok)
	assert.Equal(t, expected.Status, headEvent.Status)
	assert.True(t, expected.HeadData.Equals(headEvent.Head))
}

func assertUpstreamStateMatches(t *testing.T, expected, actual protocol.UpstreamState) {
	t.Helper()

	assert.Equal(t, expected.Status, actual.Status)
	assert.Equal(t, expected.HeadData, actual.HeadData)
	assert.Equal(t, expected.UpstreamIndex, actual.UpstreamIndex)
	assert.Equal(t, expected.RateLimiterBudget, actual.RateLimiterBudget)
	assert.Equal(t, expected.AutoTuneRateLimiter, actual.AutoTuneRateLimiter)
	assert.Equal(t, expected.BlockInfo.GetBlocks(), actual.BlockInfo.GetBlocks())
	assert.ElementsMatch(t, expected.LowerBoundsInfo.GetAllBounds(), actual.LowerBoundsInfo.GetAllBounds())
	assert.Equal(t, expected.Labels.GetAllLabels(), actual.Labels.GetAllLabels())
	assert.True(t, expected.Caps.Equal(actual.Caps))
	assert.True(t, expected.UpstreamMethods.GetSupportedMethods().Equal(actual.UpstreamMethods.GetSupportedMethods()))
}

func assertNoUpstreamEvent(t *testing.T, sub *utils.Subscription[protocol.UpstreamEvent]) {
	t.Helper()

	select {
	case event := <-sub.Events:
		t.Fatalf("unexpected upstream event: %#v", event)
	case <-time.After(60 * time.Millisecond):
	}
}

func loadMethodSpecs(t *testing.T) {
	t.Helper()

	loadMethodSpecsOnce.Do(func() {
		err := specs.NewMethodSpecLoader().Load()
		require.NoError(t, err)
	})
}

func mustNewUpstreamMethods(t *testing.T, methodsConfig *config.MethodsConfig) methods.Methods {
	t.Helper()
	loadMethodSpecs(t)

	if methodsConfig == nil {
		methodsConfig = &config.MethodsConfig{}
	}

	upstreamMethods, err := methods.NewUpstreamMethods("eth", methodsConfig)
	require.NoError(t, err)
	return upstreamMethods
}
