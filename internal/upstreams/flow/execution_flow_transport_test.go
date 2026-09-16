package flow

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTransportExec(t *testing.T, chain chains.Chain) *GenericExecutionFlow {
	t.Helper()
	specs_utils.LoadMethodSpecs()

	chSup := test_utils.CreateChainSupervisor()
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", chain).Return(chSup)
	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())

	registry := rating.NewRatingRegistry(upSupervisor, nil, &config.ScorePolicyConfig{
		CalculationFunctionName: config.DefaultLatencyPolicyFuncName,
		CalculationInterval:     1 * time.Minute,
	})

	cacheProcessor := mocks.NewCacheProcessorMock()
	cacheProcessor.On("Receive", mock.Anything, mock.Anything, mock.Anything).Return([]byte(nil), false)
	cacheProcessor.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

	return &GenericExecutionFlow{
		chain:              chain,
		upstreamSupervisor: upSupervisor,
		cacheProcessor:     cacheProcessor,
		registry:           registry,
		responseChan:       make(chan *protocol.ResponseHolderWrapper),
		appConfig:          &config.AppConfig{UpstreamConfig: &config.UpstreamConfig{IntegrityConfig: &config.IntegrityConfig{}}},
	}
}

func executeSingle(t *testing.T, exec *GenericExecutionFlow, request protocol.RequestHolder) *protocol.ResponseHolderWrapper {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go exec.Execute(ctx, []protocol.RequestHolder{request})

	select {
	case wrapper, ok := <-exec.GetResponses():
		require.True(t, ok, "flow closed without a response")
		return wrapper
	case <-ctx.Done():
		t.Fatal("no response from the flow")
		return nil
	}
}

func TestExecuteRejectsJsonRpcRequestForGrpcOnlyMethod(t *testing.T) {
	exec := newTransportExec(t, chains.SUI)
	request := protocol.NewUpstreamJsonRpcRequest(
		"1",
		protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "/sui.rpc.v2.LedgerService/GetServiceInfo"},
		false,
		"sui",
	)

	wrapper := executeSingle(t, exec, request)

	assert.Equal(t, NoUpstream, wrapper.UpstreamId)
	require.True(t, wrapper.Response.HasError())
	assert.Equal(t, protocol.NoSupportedMethod, wrapper.Response.GetError().Code)
	assert.Equal(t, "the method /sui.rpc.v2.LedgerService/GetServiceInfo is not available over json-rpc", wrapper.Response.GetError().Message)
}

func TestExecuteLetsJsonRpcRequestForTendermintMethodThrough(t *testing.T) {
	exec := newTransportExec(t, chains.COSMOS_HUB)
	request := protocol.NewUpstreamJsonRpcRequest(
		"1",
		protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "status"},
		false,
		"cosmos",
	)

	wrapper := executeSingle(t, exec, request)

	// no upstreams are configured, so the request reaches the strategy and
	// fails there - the transport gate must not have stopped it earlier
	require.True(t, wrapper.Response.HasError())
	assert.Equal(t, protocol.NoAvailableUpstreams, wrapper.Response.GetError().Code, wrapper.Response.GetError().Message)
}
