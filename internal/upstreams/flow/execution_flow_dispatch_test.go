package flow

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateRequestProcessorUsesFanoutForDispatchMethods(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	exec := &BaseExecutionFlow{
		chain: chains.ETHEREUM,
		appConfig: &config.AppConfig{UpstreamConfig: &config.UpstreamConfig{
			IntegrityConfig: &config.IntegrityConfig{},
			ChainDefaults: map[string]*config.ChainDefaults{
				chains.ETHEREUM.String(): {Dispatch: &config.DispatchOptions{Broadcast: lo.ToPtr(true)}},
			},
		}},
	}
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_sendRawTransaction"}, false, "eth")

	processor := exec.createRequestProcessor(request)

	assert.IsType(t, &FanoutRequestProcessor{}, processor)
}

func TestCreateRequestProcessorUsesFanoutForMaximumValueDispatchMethods(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	exec := &BaseExecutionFlow{
		chain: chains.ETHEREUM,
		appConfig: &config.AppConfig{UpstreamConfig: &config.UpstreamConfig{
			IntegrityConfig: &config.IntegrityConfig{},
			ChainDefaults: map[string]*config.ChainDefaults{
				chains.ETHEREUM.String(): {Dispatch: &config.DispatchOptions{MaximumValue: lo.ToPtr(true)}},
			},
		}},
	}
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionCount"}, false, "eth")

	processor := exec.createRequestProcessor(request)

	assert.IsType(t, &FanoutRequestProcessor{}, processor)
}

func TestCreateRequestProcessorKeepsUnaryForFanoutDispatchDisabled(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	exec := &BaseExecutionFlow{
		chain: chains.ETHEREUM,
		appConfig: &config.AppConfig{UpstreamConfig: &config.UpstreamConfig{
			IntegrityConfig: &config.IntegrityConfig{},
			Mode:            config.DefaultMode,
		}},
	}
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_sendRawTransaction"}, false, "eth")

	processor := exec.createRequestProcessor(request)

	cacheProc, ok := processor.(*CacheRequestProcessor)
	require.True(t, ok)
	assert.IsType(t, &UnaryRequestProcessor{}, cacheProc.delegate)
}

func TestCreateStrategyRejectsQuorumForDispatchMethods(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", chains.ETHEREUM).Return(nil).Once()
	exec := &BaseExecutionFlow{
		chain:              chains.ETHEREUM,
		upstreamSupervisor: upSupervisor,
	}
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_sendRawTransaction"}, false, "eth")
	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 2, QuorumOf: 3})

	strategy := exec.createStrategy(ctx, request)
	_, err := strategy.SelectUpstream(request)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dispatch methods")
}

func TestCreateRequestProcessorKeepsUnaryForDefaultMethods(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	exec := &BaseExecutionFlow{
		chain:     chains.ETHEREUM,
		appConfig: &config.AppConfig{UpstreamConfig: &config.UpstreamConfig{IntegrityConfig: &config.IntegrityConfig{}}},
	}
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}, false, "eth")

	processor := exec.createRequestProcessor(request)

	cacheProc, ok := processor.(*CacheRequestProcessor)
	require.True(t, ok)
	assert.IsType(t, &UnaryRequestProcessor{}, cacheProc.delegate)
}

func TestCreateRequestProcessorKeepsUnaryForNotNullDispatchDisabled(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	exec := &BaseExecutionFlow{
		chain: chains.ETHEREUM,
		appConfig: &config.AppConfig{UpstreamConfig: &config.UpstreamConfig{
			IntegrityConfig: &config.IntegrityConfig{},
			Mode:            config.DefaultMode,
		}},
	}
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionByHash"}, false, "eth")

	processor := exec.createRequestProcessor(request)

	cacheProc, ok := processor.(*CacheRequestProcessor)
	require.True(t, ok)
	assert.IsType(t, &UnaryRequestProcessor{}, cacheProc.delegate)
}

func TestCreateRequestProcessorUsesNotNullWhenEnabled(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	exec := &BaseExecutionFlow{
		chain: chains.ETHEREUM,
		appConfig: &config.AppConfig{UpstreamConfig: &config.UpstreamConfig{
			IntegrityConfig: &config.IntegrityConfig{},
			ChainDefaults: map[string]*config.ChainDefaults{
				chains.ETHEREUM.String(): {Dispatch: &config.DispatchOptions{NotNull: lo.ToPtr(true)}},
			},
		}},
	}
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getTransactionByHash"}, false, "eth")

	processor := exec.createRequestProcessor(request)

	cacheProc, ok := processor.(*CacheRequestProcessor)
	require.True(t, ok)
	assert.IsType(t, &NotNullRequestProcessor{}, cacheProc.delegate)
}
