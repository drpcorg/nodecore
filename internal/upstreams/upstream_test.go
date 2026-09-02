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
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/evm_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var loadMethodSpecsOnce sync.Once

func TestGenericUpstreamStart_WithoutProcessors_PublishesAvailableState(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)
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

func TestGenericUpstreamStop_StopsRunningLifecycle(t *testing.T) {
	upstream, _, _ := newTestGenericUpstream(t, nil, nil, nil)

	upstream.Start()
	require.True(t, upstream.Running())

	upstream.Stop()

	assert.False(t, upstream.Running())
}

func TestGenericUpstreamProcessStateEvents_UpdatesHeadState(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_UpdatesBlockState(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_IgnoresDuplicateBlockState(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_UpdatesLowerBoundsState(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_IgnoresDuplicateLowerBoundsState(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_UpdatesLabelsState(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_IgnoresDuplicateStatusState(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_IgnoresDuplicateLabelsState(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_DuplicateHeadStateStillPublishes(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_AppliesCaps(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	emit(&protocol.CapsUpstreamStateEvent{
		Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.PendingTxCap),
	})

	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.PendingTxCap),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestGenericUpstreamProcessStateEvents_IgnoresDuplicateCaps(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	capsEvent := &protocol.CapsUpstreamStateEvent{
		Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.PendingTxCap),
	}

	emit(capsEvent)
	event := nextUpstreamEvent(t, sub)
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, nil),
		mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.PendingTxCap),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available
	assertStateEventMatches(t, event, expectedState)

	emit(capsEvent)
	assertNoUpstreamEvent(t, sub)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestGenericUpstreamProcessStateEvents_ClearsCaps(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	emit(&protocol.CapsUpstreamStateEvent{
		Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.PendingTxCap),
	})
	_ = nextUpstreamEvent(t, sub)

	emit(&protocol.CapsUpstreamStateEvent{Caps: mapset.NewThreadUnsafeSet[protocol.Cap]()})

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

func TestGenericUpstreamProcessStateEvents_IgnoresDuplicateClearedCaps(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	emit(&protocol.CapsUpstreamStateEvent{
		Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.PendingTxCap),
	})
	_ = nextUpstreamEvent(t, sub)

	clearedEvent := &protocol.CapsUpstreamStateEvent{Caps: mapset.NewThreadUnsafeSet[protocol.Cap]()}
	emit(clearedEvent)
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

	emit(clearedEvent)
	assertNoUpstreamEvent(t, sub)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestGenericUpstreamProcessStateEvents_FatalErrorSuppressesStateUntilValid(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_IgnoresDuplicateFatalErrorState(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_IgnoresDuplicateValidState(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamProcessStateEvents_IgnoresDuplicateValidStateAfterRecovery(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

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

func TestGenericUpstreamBanMethod_BansAndUnbansMethod(t *testing.T) {
	loadMethodSpecs(t)

	upConfig := newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
	upstream, _, sub := newTestGenericUpstream(t, upConfig, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)
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

func TestGenericUpstreamBanMethod_IgnoresEnabledMethod(t *testing.T) {
	loadMethodSpecs(t)

	upConfig := newUpstreamConfig(&config.MethodsConfig{
		BanDuration:   20 * time.Millisecond,
		EnableMethods: []string{"eth_call"},
	})
	upstream, _, sub := newTestGenericUpstream(t, upConfig, nil, nil)

	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	upstream.BanMethod("eth_call")

	assertNoUpstreamEvent(t, sub)
	assert.True(t, upstream.GetUpstreamState().UpstreamMethods.HasMethod("eth_call"))
}

func TestGenericUpstreamGetConnector_ReturnsMatchingConnector(t *testing.T) {
	httpConnector := mocks.NewConnectorMockWithType(specs.JsonRpcConnector)
	wsConnector := mocks.NewConnectorMockWithType(specs.WebsocketConnector)

	upstream, _, _ := newTestGenericUpstream(t, nil, []*mocks.ConnectorMock{httpConnector, wsConnector}, nil)

	assert.Same(t, httpConnector, upstream.GetConnector(specs.JsonRpcConnector))
	assert.Same(t, wsConnector, upstream.GetConnector(specs.WebsocketConnector))
	assert.Nil(t, upstream.GetConnector(specs.RestConnector))
}

func TestGenericUpstreamUpdateHead_DelegatesToHeadProcessor(t *testing.T) {
	headProcessor := mocks.NewHeadProcessorMock()
	headProcessor.On("UpdateHead", uint64(100), uint64(7)).Once()

	headEventProcessor := event_processors.NewHeadEventProcessor(context.Background(), "id", chains.ETHEREUM, headProcessor)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{headEventProcessor})
	upstream, _, _ := newTestGenericUpstream(t, nil, nil, aggregator)

	upstream.UpdateHead(100, 7)

	headProcessor.AssertExpectations(t)
}

func TestGenericUpstreamUpdateHead_DelegatesToBlockProcessor(t *testing.T) {
	blockProcessor := mocks.NewBlockProcessorMock()
	blockData := protocol.NewBlock(uint64(1002), 0, blockchain.EmptyHash, blockchain.EmptyHash)
	blockProcessor.On("UpdateBlock", blockData, protocol.FinalizedBlock).Once()

	blockEventProcessor := event_processors.NewGenericBlockEventProcessor(context.Background(), "id", chains.ETHEREUM, blockProcessor)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{blockEventProcessor})
	upstream, _, _ := newTestGenericUpstream(t, nil, nil, aggregator)

	upstream.UpdateBlock(blockData, protocol.FinalizedBlock)

	blockProcessor.AssertExpectations(t)
}

func TestGenericUpstreamStart_WithFatalSettingsValidation_DoesNotRun(t *testing.T) {
	validator := mocks.NewSettingsValidatorMock()
	validator.On("Validate").Return(validations.FatalSettingError).Once()

	upConfig := newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
	settingsProcessor := event_processors.NewGenericSettingsEventProcessor(
		context.Background(),
		"id",
		testUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator}),
	)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{settingsProcessor})
	upstream, _, _ := newTestGenericUpstream(t, upConfig, nil, aggregator)

	upstream.Start()

	assert.False(t, upstream.Running())
	validator.AssertExpectations(t)
}

func TestGenericUpstreamStart_WithSettingsError_KeepsRunningWithoutPublishingState(t *testing.T) {
	validator := mocks.NewSettingsValidatorMock()
	validator.On("Validate").Return(validations.SettingsError)

	settingsProcessor := event_processors.NewGenericSettingsEventProcessor(
		context.Background(),
		"id",
		testUpstreamOptions(),
		validations.NewSettingsValidationProcessor([]validations.Validator[validations.ValidationSettingResult]{validator}),
	)
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{settingsProcessor})
	upstream, _, sub := newTestGenericUpstream(t, nil, nil, aggregator)

	t.Cleanup(upstream.Stop)

	upstream.Start()

	assert.True(t, upstream.Running())
	assertNoUpstreamEvent(t, sub)
}

func TestGenericUpstreamProcessStateEvents_HeadLagDrivesSyncing(t *testing.T) {
	// The test upstream is on ethereum, whose syncing lag threshold is 6.
	upstream, _, sub := newTestGenericUpstream(t, nil, nil, nil)
	t.Cleanup(upstream.Stop)
	startUpstream(t, upstream, sub)

	// setLag beyond the threshold downgrades the derived status to Syncing while
	// preserving the base availability.
	upstream.UpdateHeadLag(100)
	event := nextUpstreamEvent(t, sub)
	stateEvent, ok := event.EventType.(*protocol.StateUpstreamEvent)
	require.True(t, ok)
	assert.Equal(t, protocol.Syncing, stateEvent.State.Status)
	assert.Equal(t, protocol.Syncing, upstream.GetUpstreamState().Status)

	// setLag back within the threshold restores Available (recovery).
	upstream.UpdateHeadLag(1)
	event = nextUpstreamEvent(t, sub)
	stateEvent, ok = event.EventType.(*protocol.StateUpstreamEvent)
	require.True(t, ok)
	assert.Equal(t, protocol.Available, stateEvent.State.Status)
	assert.Equal(t, protocol.Available, upstream.GetUpstreamState().Status)
}

func TestGenericUpstreamProcessStateEvents_HeadLagDoesNotUpgradeUnavailable(t *testing.T) {
	upstream, emit, sub := newTestGenericUpstream(t, nil, nil, nil)
	t.Cleanup(upstream.Stop)
	startUpstream(t, upstream, sub)

	emit(&protocol.StatusUpstreamStateEvent{Status: protocol.Unavailable})
	_ = nextUpstreamEvent(t, sub)
	require.Equal(t, protocol.Unavailable, upstream.GetUpstreamState().Status)

	// A small lag (setLag) must not upgrade an Unavailable upstream; the derived
	// status stays Unavailable, so nothing new is published.
	upstream.UpdateHeadLag(1)
	assertNoUpstreamEvent(t, sub)
	assert.Equal(t, protocol.Unavailable, upstream.GetUpstreamState().Status)
}

func TestGenericUpstreamPredictLowerBound_CapsUpperEdgeAtHead(t *testing.T) {
	lowerBoundProcessor := mocks.NewLowerBoundProcessorMock()
	lowerBoundProcessor.On("PredictLowerBound", protocol.UpperProofBound, int64(0)).Return(int64(1010))
	lowerBoundProcessor.On("PredictLowerBound", protocol.StateBound, int64(0)).Return(int64(1010))
	aggregator := event_processors.NewUpstreamProcessorAggregator([]event_processors.UpstreamStateEventProcessor{
		event_processors.NewGenericLowerBoundEventProcessor(context.Background(), "id", lowerBoundProcessor),
	})
	upstream := newTestGenericUpstreamWithHead(t, aggregator, 1000)

	assert.Equal(t, int64(1000), upstream.PredictLowerBound(protocol.UpperProofBound, 0), "an upper edge cannot be ahead of the node itself")
	assert.Equal(t, int64(1010), upstream.PredictLowerBound(protocol.StateBound, 0), "a lower edge is not capped by the head")
	lowerBoundProcessor.AssertExpectations(t)
}

// newTestGenericUpstreamWithHead builds an upstream whose stored state already has a head,
// without starting the event loop: PredictLowerBound reads the stored state directly.
func newTestGenericUpstreamWithHead(
	t *testing.T,
	aggregator *event_processors.UpstreamProcessorAggregator,
	head uint64,
) *upstreams.GenericUpstream {
	t.Helper()
	loadMethodSpecs(t)

	upConfig := newUpstreamConfig(nil)
	upstreamMethods, err := methods.NewUpstreamMethods("eth", upConfig.Methods, nil)
	require.NoError(t, err)

	upstreamState := protocol.DefaultUpstreamState(
		upstreamMethods,
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	upstreamState.HeadData = protocol.NewBlockWithHeight(head)
	state := utils.NewAtomic[protocol.UpstreamState]()
	state.Store(upstreamState)

	stateChan := make(chan protocol.AbstractUpstreamStateEvent, 100)
	emitter := func(event protocol.AbstractUpstreamStateEvent) {
		stateChan <- event
	}
	var stateEmitter event_processors.Emitter = emitter

	return upstreams.NewGenericUpstreamWithParams(
		"id",
		chains.ETHEREUM,
		nil,
		upConfig,
		"00012",
		state,
		aggregator,
		&stateChan,
		&stateEmitter,
	)
}

func newTestGenericUpstream(
	t *testing.T,
	upConfig *config.Upstream,
	connectorMocks []*mocks.ConnectorMock,
	aggregator *event_processors.UpstreamProcessorAggregator,
) (*upstreams.GenericUpstream, func(protocol.AbstractUpstreamStateEvent), *utils.Subscription[protocol.UpstreamEvent]) {
	t.Helper()
	loadMethodSpecs(t)

	if upConfig == nil {
		upConfig = newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
	}

	upstreamMethods, err := methods.NewUpstreamMethods("eth", upConfig.Methods, nil)
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

	upstream := upstreams.NewGenericUpstreamWithParams(
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

// startUpstream calls upstream.Start() and drains the InitUpstreamStateEvent
// that Start emits at the end of its lifecycle to announce the upstream's
// initial state to subscribers. Tests that exercise post-Start event flows
// use this so the announcement doesn't masquerade as their expected event.
func startUpstream(t *testing.T, upstream *upstreams.GenericUpstream, sub *utils.Subscription[protocol.UpstreamEvent]) {
	t.Helper()
	upstream.Start()
	event := nextUpstreamEvent(t, sub)
	_, ok := event.EventType.(*protocol.StateUpstreamEvent)
	require.Truef(t, ok, "expected initial StateUpstreamEvent after Start, got: %T", event.EventType)
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

	upstreamMethods, err := methods.NewUpstreamMethods("eth", methodsConfig, nil)
	require.NoError(t, err)
	return upstreamMethods
}

func TestGenericUpstreamUnsupportedMethods_StripsDetectedMethod(t *testing.T) {
	upConfig := newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
	upstream, emit, sub := newTestGenericUpstream(t, upConfig, nil, nil)
	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	emit(&protocol.UnsupportedMethodsUpstreamStateEvent{
		Methods: mapset.NewThreadUnsafeSet[string]("trace_block"),
	})

	// Detection subtracts exactly like a disable does, so the resulting set is the one
	// the spec would produce with trace_block disabled.
	expectedState := protocol.DefaultUpstreamState(
		mustNewUpstreamMethods(t, &config.MethodsConfig{
			BanDuration:    upConfig.Methods.BanDuration,
			EnableMethods:  upConfig.Methods.EnableMethods,
			DisableMethods: []string{"trace_block"},
		}),
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"00012",
		nil,
		nil,
	)
	expectedState.Status = protocol.Available

	event := nextUpstreamEvent(t, sub)
	assertStateEventMatches(t, event, expectedState)
	assertUpstreamStateMatches(t, expectedState, upstream.GetUpstreamState())
}

func TestGenericUpstreamUnsupportedMethods_IdenticalSetIsNotRepublished(t *testing.T) {
	upConfig := newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
	upstream, emit, sub := newTestGenericUpstream(t, upConfig, nil, nil)
	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	unsupported := mapset.NewThreadUnsafeSet[string]("trace_block")
	emit(&protocol.UnsupportedMethodsUpstreamStateEvent{Methods: unsupported})
	nextUpstreamEvent(t, sub)

	// A re-detection round that finds the same thing must not wake subscribers.
	emit(&protocol.UnsupportedMethodsUpstreamStateEvent{Methods: unsupported.Clone()})
	assertNoUpstreamEvent(t, sub)
}

func TestGenericUpstreamUnsupportedMethods_SurvivesAnUnban(t *testing.T) {
	upConfig := newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
	upstream, emit, sub := newTestGenericUpstream(t, upConfig, nil, nil)
	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	emit(&protocol.UnsupportedMethodsUpstreamStateEvent{
		Methods: mapset.NewThreadUnsafeSet[string]("trace_block"),
	})
	nextUpstreamEvent(t, sub)

	// A different method is banned and then auto-unbanned after BanDuration. The unban
	// must restore only the banned method, never the detected one.
	upstream.BanMethod("eth_call")
	nextUpstreamEvent(t, sub)
	nextUpstreamEvent(t, sub)

	supported := upstream.GetUpstreamState().UpstreamMethods.GetSupportedMethods()
	assert.True(t, supported.ContainsOne("eth_call"), "the ban must have expired")
	assert.False(t, supported.ContainsOne("trace_block"), "an unban must not resurrect an unsupported method")
}

func TestGenericUpstreamUnsupportedMethods_ConfigEnableWins(t *testing.T) {
	upConfig := newUpstreamConfig(&config.MethodsConfig{
		BanDuration:   20 * time.Millisecond,
		EnableMethods: []string{"trace_block"},
	})
	upstream, emit, sub := newTestGenericUpstream(t, upConfig, nil, nil)
	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	emit(&protocol.UnsupportedMethodsUpstreamStateEvent{
		Methods: mapset.NewThreadUnsafeSet[string]("trace_block"),
	})

	require.Eventually(t, func() bool {
		return upstream.GetUpstreamState().UpstreamMethods.GetSupportedMethods().ContainsOne("trace_block")
	}, time.Second, 10*time.Millisecond, "config enable is applied last and must outrank detection")
}

func TestGenericUpstreamStartsMethodsDetectionAndNarrowsTheMethodSet(t *testing.T) {
	methodsProcessor := mocks.NewMethodsProcessorMock()
	methodsProcessor.On("Start").Return()
	methodsProcessor.On("Subscribe", "id_methods").Return()
	methodsProcessor.On("Stop").Return()

	upConfig := newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
	methodsEventProcessor := event_processors.NewMethodsEventProcessor(context.Background(), upConfig.Id, methodsProcessor)
	require.NotNil(t, methodsEventProcessor)

	aggregator := event_processors.NewUpstreamProcessorAggregator(
		[]event_processors.UpstreamStateEventProcessor{methodsEventProcessor},
	)

	upstream, _, sub := newTestGenericUpstream(t, upConfig, nil, aggregator)
	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	// Resume() runs inside Start(), so the processor must be running by now.
	require.True(t, methodsEventProcessor.Running(), "Resume must start the methods event processor")

	before := upstream.GetUpstreamState().UpstreamMethods.GetSupportedMethods()
	require.True(t, before.ContainsOne("trace_block"), "the upstream starts with the full spec set")

	methodsProcessor.Publish(mapset.NewThreadUnsafeSet[string]("trace_block"))

	require.Eventually(t, func() bool {
		supported := upstream.GetUpstreamState().UpstreamMethods.GetSupportedMethods()
		// Locally-served methods are never detectable, so they must survive regardless.
		return !supported.ContainsOne("trace_block") &&
			supported.ContainsOne("net_version") &&
			supported.ContainsOne("eth_chainId")
	}, 2*time.Second, 10*time.Millisecond)

	// Not AssertExpectations: Stop is registered so the t.Cleanup teardown does not panic
	// on an unexpected call, but that teardown runs after this body, so Stop has not been
	// called yet here. MethodsEventProcessor's own test covers the Stop delegation.
	methodsProcessor.AssertCalled(t, "Start")
	methodsProcessor.AssertCalled(t, "Subscribe", "id_methods")
}

func TestCreateMethodsEventProcessorRespectsTheOption(t *testing.T) {
	loadMethodSpecs(t)

	build := func(disabled bool) event_processors.UpstreamStateEventProcessor {
		conf := newUpstreamConfig(&config.MethodsConfig{BanDuration: 20 * time.Millisecond})
		conf.Options.DisableMethodsDetection = new(disabled)

		connector := mocks.NewConnectorMock()
		connector.On("GetType").Return(specs.JsonRpcConnector).Maybe()

		chainSpecific := evm_specific.NewEvmChainSpecific(
			context.Background(),
			conf.Id,
			connector,
			[]connectors.ApiConnector{connector},
			chains.GetChain(chains.ETHEREUM.String()),
			time.Second,
			conf.Options,
			nil,
		)

		return upstreams.CreateMethodsEventProcessor(context.Background(), conf, chainSpecific)
	}

	assert.Nil(t, build(true), "the option must switch the whole pipeline off")
	assert.NotNil(t, build(false))
}

func TestGenericUpstreamUnsupportedMethods_GroupEnableWins(t *testing.T) {
	// `enable: [trace]` is a group name, not a method name, so a name-list check would
	// miss it. The composition still re-enables trace_block, and the warning must fire.
	upConfig := newUpstreamConfig(&config.MethodsConfig{
		BanDuration:   20 * time.Millisecond,
		EnableMethods: []string{"trace"},
	})
	upstream, emit, sub := newTestGenericUpstream(t, upConfig, nil, nil)
	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	emit(&protocol.UnsupportedMethodsUpstreamStateEvent{
		Methods: mapset.NewThreadUnsafeSet[string]("trace_block"),
	})

	require.Eventually(t, func() bool {
		return upstream.GetUpstreamState().UpstreamMethods.GetSupportedMethods().ContainsOne("trace_block")
	}, time.Second, 10*time.Millisecond, "a group enable outranks detection just as a name enable does")
}

func TestGenericUpstreamBanMethod_GroupEnabledMethodIsNotBanned(t *testing.T) {
	// The ban would be undone by the group enable, so recording it would only schedule a
	// pointless unban and re-arm on the next failure.
	upConfig := newUpstreamConfig(&config.MethodsConfig{
		BanDuration:   20 * time.Millisecond,
		EnableMethods: []string{"trace"},
	})
	upstream, _, sub := newTestGenericUpstream(t, upConfig, nil, nil)
	t.Cleanup(upstream.Stop)

	startUpstream(t, upstream, sub)

	upstream.BanMethod("trace_block")

	assertNoUpstreamEvent(t, sub)
	assert.True(t,
		upstream.GetUpstreamState().UpstreamMethods.GetSupportedMethods().ContainsOne("trace_block"),
		"the method stays enabled, so nothing should have changed",
	)
}
