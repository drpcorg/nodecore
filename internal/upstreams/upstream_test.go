package upstreams_test

import (
	"context"
	"errors"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestUpstreamHeadEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connector := mocks.NewConnectorMock()
	bodyLatest := []byte(`{
	  "jsonrpc": "2.0",
	  "result": {
		"number": "0x41fd60b",
		"hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"
	  }
	}`)
	requestLatest, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthGetBlockByNumber, []any{"latest", false})
	responseLatest := protocol.NewHttpUpstreamResponse("1", bodyLatest, 200, protocol.JsonRpc)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(requestLatest))).
		Return(responseLatest)

	upConfig := &config.Upstream{
		Id:           "id",
		PollInterval: 50 * time.Millisecond,
		Options:      &config.UpstreamOptions{InternalTimeout: 5 * time.Second},
	}

	upstream := test_utils.TestEvmUpstream(ctx, connector, upConfig, nil, nil, mocks.NewMethodsMock())
	go upstream.Start()

	sub := upstream.Subscribe("name")

	checkFunc := func(height uint64, hash string) {
		event, ok := <-sub.Events
		state := protocol.UpstreamState{
			Status: protocol.Available,
			HeadData: &protocol.BlockData{
				Height: height,
				Hash:   hash,
			},
			BlockInfo:       protocol.NewBlockInfo(),
			UpstreamMethods: mocks.NewMethodsMock(),
			Caps:            mapset.NewThreadUnsafeSet[protocol.Cap](),
			UpstreamIndex:   "00012",
		}
		expected := protocol.UpstreamEvent{
			Id:    "id",
			Chain: chains.ETHEREUM,
			EventType: &protocol.StateUpstreamEvent{
				State: &state,
			},
		}

		assert.True(t, ok)
		assert.Equal(t, expected, event)
		assert.Equal(t, state, upstream.GetUpstreamState())
	}

	checkFunc(69195275, "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18")
	upstream.UpdateHead(79195275, 0)
	checkFunc(79195275, "")
	connector.AssertExpectations(t)

}

func TestUpstreamBlockEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connector := mocks.NewConnectorMock()
	bodyFinalized := []byte(`{
	 "jsonrpc": "2.0",
	 "result": {
		"number": "0x345",
		"hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"
	 }
	}`)
	requestLatest, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthGetBlockByNumber, []any{"latest", false})
	responseLatest := protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.TotalFailure)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(requestLatest))).
		Return(responseLatest)

	requestFinalized, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthGetBlockByNumber, []any{"finalized", false})
	responseFinalized := protocol.NewHttpUpstreamResponse("1", bodyFinalized, 200, protocol.JsonRpc)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(requestFinalized))).
		Return(responseFinalized)

	upConfig := &config.Upstream{
		Id:           "id",
		PollInterval: 50 * time.Millisecond,
		Options:      &config.UpstreamOptions{InternalTimeout: 5 * time.Second},
	}

	blockProcessor := blocks.NewEthLikeBlockProcessor(ctx, upConfig, connector, &specific.EvmChainSpecificObject{})

	upstream := test_utils.TestEvmUpstream(ctx, connector, upConfig, blockProcessor, nil, mocks.NewMethodsMock())
	go upstream.Start()

	sub := upstream.Subscribe("name")

	checkFunc := func(blockData *protocol.BlockData) {
		event, ok := <-sub.Events
		blockInfo := protocol.NewBlockInfo()
		blockInfo.AddBlock(
			blockData,
			protocol.FinalizedBlock,
		)
		state := protocol.UpstreamState{
			Status:          protocol.Unavailable,
			HeadData:        &protocol.BlockData{},
			BlockInfo:       blockInfo,
			UpstreamMethods: mocks.NewMethodsMock(),
			Caps:            mapset.NewThreadUnsafeSet[protocol.Cap](),
			UpstreamIndex:   "00012",
		}
		expected := protocol.UpstreamEvent{
			Id:    "id",
			Chain: chains.ETHEREUM,
			EventType: &protocol.StateUpstreamEvent{
				State: &state,
			},
		}

		assert.True(t, ok)
		assert.EqualExportedValues(t, expected, event)
		assert.EqualExportedValues(t, state, upstream.GetUpstreamState())

		eventType, ok := event.EventType.(*protocol.StateUpstreamEvent)
		assert.True(t, ok)
		assert.Equal(t, state.BlockInfo.GetBlocks(), eventType.State.BlockInfo.GetBlocks())
		assert.Equal(t, state.BlockInfo.GetBlocks(), upstream.GetUpstreamState().BlockInfo.GetBlocks())
	}

	checkFunc(protocol.NewBlockData(837, 0, "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"))
	upstream.UpdateBlock(protocol.NewBlockDataWithHeight(1000), protocol.FinalizedBlock)
	checkFunc(protocol.NewBlockData(1000, 0, ""))

	time.Sleep(15 * time.Millisecond)

	connector.AssertExpectations(t)
}

func TestUpstreamMethodEvents(t *testing.T) {
	err := specs.NewMethodSpecLoader().Load()
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connector := mocks.NewConnectorMock()
	requestLatest, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthGetBlockByNumber, []any{"latest", false})
	responseLatest := protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.TotalFailure)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(requestLatest))).
		Return(responseLatest)

	upConfig := &config.Upstream{
		Id:           "id",
		PollInterval: 50 * time.Millisecond,
		Methods: &config.MethodsConfig{
			BanDuration: 20 * time.Millisecond,
		},
		Options: &config.UpstreamOptions{InternalTimeout: 5 * time.Second},
	}

	upstreamMethods, err := methods.NewUpstreamMethods("eth", &config.MethodsConfig{})
	assert.NoError(t, err)

	upstream := test_utils.TestEvmUpstream(ctx, connector, upConfig, nil, nil, upstreamMethods)
	go upstream.Start()
	go func() {
		time.Sleep(10 * time.Millisecond)
		upstream.BanMethod("eth_call")
		time.Sleep(10 * time.Millisecond)
		upstream.BanMethod("eth_getLogs")
	}()

	sub := upstream.Subscribe("name")

	upstreamMethods, _ = methods.NewUpstreamMethods("eth", &config.MethodsConfig{DisableMethods: []string{"eth_call"}})
	checkMethods(t, upstream, sub, upstreamMethods)
	upstreamMethods, _ = methods.NewUpstreamMethods("eth", &config.MethodsConfig{DisableMethods: []string{"eth_call", "eth_getLogs"}})
	checkMethods(t, upstream, sub, upstreamMethods)
	upstreamMethods, _ = methods.NewUpstreamMethods("eth", &config.MethodsConfig{DisableMethods: []string{"eth_getLogs"}})
	checkMethods(t, upstream, sub, upstreamMethods)
	upstreamMethods, _ = methods.NewUpstreamMethods("eth", &config.MethodsConfig{})
	checkMethods(t, upstream, sub, upstreamMethods)
}

func TestUpstreamMethodEventsPreserveMethodFromConfig(t *testing.T) {
	err := specs.NewMethodSpecLoader().Load()
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connector := mocks.NewConnectorMock()
	requestLatest, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthGetBlockByNumber, []any{"latest", false})
	responseLatest := protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.TotalFailure)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(requestLatest))).
		Return(responseLatest)

	upConfig := &config.Upstream{
		Id:           "id",
		PollInterval: 50 * time.Millisecond,
		Methods: &config.MethodsConfig{
			BanDuration:    20 * time.Millisecond,
			EnableMethods:  []string{"test", "test2", "test3"},
			DisableMethods: []string{"test4", "test5"},
		},
		Options: &config.UpstreamOptions{InternalTimeout: 5 * time.Second},
	}

	upstreamMethods, err := methods.NewUpstreamMethods("eth", &config.MethodsConfig{})
	assert.NoError(t, err)

	upstream := test_utils.TestEvmUpstream(ctx, connector, upConfig, nil, nil, upstreamMethods)
	go upstream.Start()
	go func() {
		time.Sleep(10 * time.Millisecond)
		upstream.BanMethod("eth_call")
		time.Sleep(10 * time.Millisecond)
		upstream.BanMethod("test3")
	}()

	sub := upstream.Subscribe("name")

	upstreamMethods, _ = methods.NewUpstreamMethods(
		"eth",
		&config.MethodsConfig{
			DisableMethods: []string{"eth_call", "test4", "test5"},
			EnableMethods:  []string{"test", "test2", "test3"},
		},
	)
	checkMethods(t, upstream, sub, upstreamMethods)
	upstreamMethods, _ = methods.NewUpstreamMethods(
		"eth",
		&config.MethodsConfig{
			DisableMethods: []string{"test4", "test5"},
			EnableMethods:  []string{"test", "test2", "test3"},
		},
	)
	checkMethods(t, upstream, sub, upstreamMethods)
}

func checkMethods(
	t *testing.T,
	upstream *upstreams.BaseUpstream,
	sub *utils.Subscription[protocol.UpstreamEvent],
	upstreamMethods *methods.UpstreamMethods,
) {
	event, ok := <-sub.Events
	state := protocol.UpstreamState{
		Status:          protocol.Unavailable,
		HeadData:        &protocol.BlockData{},
		BlockInfo:       protocol.NewBlockInfo(),
		UpstreamMethods: upstreamMethods,
		Caps:            mapset.NewThreadUnsafeSet[protocol.Cap](),
		UpstreamIndex:   "00012",
	}
	expected := protocol.UpstreamEvent{
		Id:    "id",
		Chain: chains.ETHEREUM,
		EventType: &protocol.StateUpstreamEvent{
			State: &state,
		},
	}

	assert.True(t, ok)
	assert.EqualExportedValues(t, expected, event)
	assert.EqualExportedValues(t, state, upstream.GetUpstreamState())

	eventType, ok := event.EventType.(*protocol.StateUpstreamEvent)
	assert.True(t, ok)
	assert.True(t, state.UpstreamMethods.GetSupportedMethods().Equal(eventType.State.UpstreamMethods.GetSupportedMethods()))
	assert.True(t, state.UpstreamMethods.GetSupportedMethods().Equal(upstream.GetUpstreamState().UpstreamMethods.GetSupportedMethods()))
}

func TestUpstreamFatalErrorOnStart(t *testing.T) {
	upConfig := &config.Upstream{
		Id:           "id",
		PollInterval: 50 * time.Millisecond,
		Options: &config.UpstreamOptions{
			InternalTimeout:           5 * time.Second,
			DisableValidation:         lo.ToPtr(false),
			DisableSettingsValidation: lo.ToPtr(false),
			DisableChainValidation:    lo.ToPtr(false),
		},
	}
	connector := mocks.NewConnectorMock()
	validator := mocks.NewSettingsValidatorMock()
	validator.On("Validate").Return(validations.FatalSettingError)

	settingsValidationProcessor := validations.NewSettingsValidationProcessor([]validations.SettingsValidator{validator})

	upstream := test_utils.TestEvmUpstream(context.Background(), connector, upConfig, nil, settingsValidationProcessor, mocks.NewMethodsMock())
	upstream.Start()

	assert.False(t, upstream.Running())
	assert.False(t, upstream.PartialRunning())
}

func TestUpstreamSettingErrorPartialStart(t *testing.T) {
	upConfig := &config.Upstream{
		Id:           "id",
		PollInterval: 50 * time.Millisecond,
		Options: &config.UpstreamOptions{
			InternalTimeout:           5 * time.Second,
			ValidationInterval:        30 * time.Second,
			DisableValidation:         lo.ToPtr(false),
			DisableSettingsValidation: lo.ToPtr(false),
			DisableChainValidation:    lo.ToPtr(false),
		},
	}
	connector := mocks.NewConnectorMock()
	validator := mocks.NewSettingsValidatorMock()
	validator.On("Validate").Return(validations.SettingsError)

	settingsValidationProcessor := validations.NewSettingsValidationProcessor([]validations.SettingsValidator{validator})

	upstream := test_utils.TestEvmUpstream(context.Background(), connector, upConfig, nil, settingsValidationProcessor, mocks.NewMethodsMock())
	upstream.Start()

	assert.True(t, upstream.Running())
	assert.False(t, upstream.PartialRunning())
}

func TestUpstreamValidationEvents(t *testing.T) {
	upConfig := &config.Upstream{
		Id:           "id",
		PollInterval: 50 * time.Millisecond,
		Options: &config.UpstreamOptions{
			InternalTimeout:           5 * time.Second,
			ValidationInterval:        10 * time.Millisecond,
			DisableValidation:         lo.ToPtr(false),
			DisableSettingsValidation: lo.ToPtr(false),
			DisableChainValidation:    lo.ToPtr(false),
		},
	}

	connector := mocks.NewConnectorMock()
	requestLatest, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthGetBlockByNumber, []any{"latest", false})
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(requestLatest))).
		Return(protocol.NewTotalFailureFromErr("id", errors.New("err"), protocol.JsonRpc))

	validator := mocks.NewSettingsValidatorMock()
	validator.On("Validate").Return(validations.SettingsError).Once()
	validator.On("Validate").Return(validations.Valid).Once()
	validator.On("Validate").Return(validations.Valid).Once()
	validator.On("Validate").Return(validations.FatalSettingError).Once()

	settingsValidationProcessor := validations.NewSettingsValidationProcessor([]validations.SettingsValidator{validator})

	upstream := test_utils.TestEvmUpstream(context.Background(), connector, upConfig, nil, settingsValidationProcessor, mocks.NewMethodsMock())
	upstream.Start()

	assert.True(t, upstream.Running())
	assert.False(t, upstream.PartialRunning())

	sub := upstream.Subscribe("name")

	event, ok := <-sub.Events
	assert.True(t, ok)

	_, ok = event.EventType.(*protocol.ValidUpstreamEvent)
	assert.True(t, ok)
	upstream.Resume()
	assert.True(t, upstream.Running())
	assert.True(t, upstream.PartialRunning())

	event, ok = <-sub.Events
	assert.True(t, ok)

	_, ok = event.EventType.(*protocol.RemoveUpstreamEvent)
	assert.True(t, ok)
	upstream.PartialStop()
	assert.True(t, upstream.Running())
	assert.False(t, upstream.PartialRunning())

	upstream.Stop()
	assert.False(t, upstream.Running())
	assert.False(t, upstream.PartialRunning())
}
