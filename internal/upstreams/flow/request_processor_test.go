package flow_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestUnaryRequestProcessorSubMethodThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs")).Load()
	assert.NoError(t, err)

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	chain := chains.ALEPHZERO
	jsonBody := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_subscribe"}
	request := protocol.NewUpstreamJsonRpcRequest("223", jsonBody, false, "")

	processor := flow.NewUnaryRequestProcessor(chain, upSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper

	upSupervisor.AssertNotCalled(t, "GetExecutor")
	upSupervisor.AssertNotCalled(t, "GetUpstream")
	strategy.AssertNotCalled(t, "SelectUpstream")

	assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.True(t, unaryRespWrapper.Response.HasError())
	assert.Equal(t, protocol.ResponseErrorWithData(400, "client error - unable to process a subscription request eth_subscribe", nil), unaryRespWrapper.Response.GetError())
}

func TestUnaryRequestProcessorCantGetUpstreamThenError(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	chain := chains.POLYGON
	err := errors.New("selection error")
	jsonBody := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}
	request := protocol.NewUpstreamJsonRpcRequest("223", jsonBody, false, "")

	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("", err)

	processor := flow.NewUnaryRequestProcessor(chain, upSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper

	upSupervisor.AssertNotCalled(t, "GetUpstream")
	upSupervisor.AssertExpectations(t)
	strategy.AssertExpectations(t)

	assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.True(t, unaryRespWrapper.Response.HasError())
	assert.Equal(t, protocol.ResponseErrorWithData(500, "internal server error: selection error", nil), unaryRespWrapper.Response.GetError())
}

func TestUnaryRequestProcessorNoConnectorThenError(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewConnectorMockWithType(specs.RestConnector)
	chain := chains.POLYGON
	upstream := test_utils.TestEvmUpstream(apiConnector, upConfig(), mocks.NewMethodsMock(), nil)
	jsonBody := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}
	request := protocol.NewUpstreamJsonRpcRequest("223", jsonBody, false, "")

	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)

	processor := flow.NewUnaryRequestProcessor(chain, upSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper

	upSupervisor.AssertExpectations(t)
	strategy.AssertExpectations(t)

	assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.True(t, unaryRespWrapper.Response.HasError())
	assert.Equal(t, protocol.ResponseErrorWithData(protocol.NoApiConnectors, "no suitable api connectors to process method eth_call", nil), unaryRespWrapper.Response.GetError())
}

func upConfig() *config.Upstream {
	return &config.Upstream{
		Id:           "id",
		PollInterval: 10 * time.Millisecond,
		Options:      &chains.Options{InternalTimeout: 5 * time.Second},
	}
}

type RequestProcessorMock struct {
	mock.Mock
}

func (r *RequestProcessorMock) ProcessRequest(ctx context.Context, upstreamStrategy flow.UpstreamStrategy, request protocol.RequestHolder) flow.ProcessedResponse {
	args := r.Called(ctx, upstreamStrategy, request)
	return args.Get(0).(flow.ProcessedResponse)
}

func NewRequestProcessorMock() *RequestProcessorMock {
	return &RequestProcessorMock{}
}
