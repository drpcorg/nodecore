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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var loadMethodSpecsOnce sync.Once

func TestBaseUpstreamStart_WithoutProcessors_PublishesAvailableState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	event := nextUpstreamEvent(t, sub)
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
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())

	emit(&protocol.StatusUpstreamStateEvent{Status: protocol.Unavailable})
	event = nextUpstreamEvent(t, sub)
	expectedState.Status = protocol.Unavailable
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamStop_StopsRunningLifecycle(t *testing.T) {
	upstream, _, sub := newTestBaseUpstream(t, nil, nil, nil)

	upstream.Start()
	_ = nextUpstreamEvent(t, sub)
	require.True(t, upstream.Running())

	upstream.Stop()

	assert.False(t, upstream.Running())
}

func TestBaseUpstreamProcessStateEvents_UpdatesHeadState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()
	_ = nextUpstreamEvent(t, sub)

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
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestBaseUpstreamProcessStateEvents_UpdatesBlockState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()
	_ = nextUpstreamEvent(t, sub)

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

func TestBaseUpstreamProcessStateEvents_UpdatesLowerBoundsState(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()
	_ = nextUpstreamEvent(t, sub)

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

func TestBaseUpstreamProcessStateEvents_FatalErrorSuppressesStateUntilValid(t *testing.T) {
	upstream, emit, sub := newTestBaseUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()
	_ = nextUpstreamEvent(t, sub)

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

func TestBaseUpstreamBanMethod_BansAndUnbansMethod(t *testing.T) {
	loadMethodSpecs(t)

	upConfig := newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
	upstream, _, sub := newTestBaseUpstream(t, upConfig, nil, nil)

	t.Cleanup(upstream.Stop)

	upstream.Start()
	initialEvent := nextUpstreamEvent(t, sub)
	expectedInitialState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, upConfig.Methods),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedInitialState.Status = protocol.Available
	assertStateEventMatches(t, initialEvent, expectedInitialState)

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
	_ = nextUpstreamEvent(t, sub)

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

	headEventProcessor := event_processors.NewHeadEventProcessor(context.Background(), "id", headProcessor)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{headEventProcessor})
	upstream, _, _ := newTestBaseUpstream(t, nil, nil, aggregator)

	upstream.UpdateHead(100, 7)

	headProcessor.AssertExpectations(t)
}

func TestBaseUpstreamUpdateHead_DelegatesToBlockProcessor(t *testing.T) {
	blockProcessor := mocks.NewBlockProcessorMock()
	blockData := protocol.NewBlock(uint64(1002), 0, blockchain.EmptyHash, blockchain.EmptyHash)
	blockProcessor.On("UpdateBlock", blockData, protocol.FinalizedBlock).Once()

	blockEventProcessor := event_processors.NewBaseBlockEventProcessor(context.Background(), "id", blockProcessor)
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
	if connectorMocks == nil {
		connectorMocks = []*mocks.ConnectorMock{mocks.NewConnectorMock()}
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

func assertUpstreamStateMatches(t *testing.T, expected, actual protocol.UpstreamState) {
	t.Helper()

	assert.Equal(t, expected.Status, actual.Status)
	assert.Equal(t, expected.HeadData, actual.HeadData)
	assert.Equal(t, expected.UpstreamIndex, actual.UpstreamIndex)
	assert.Equal(t, expected.RateLimiterBudget, actual.RateLimiterBudget)
	assert.Equal(t, expected.AutoTuneRateLimiter, actual.AutoTuneRateLimiter)
	assert.Equal(t, expected.BlockInfo.GetBlocks(), actual.BlockInfo.GetBlocks())
	assert.ElementsMatch(t, expected.LowerBoundsInfo.GetAllBounds(), actual.LowerBoundsInfo.GetAllBounds())
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
