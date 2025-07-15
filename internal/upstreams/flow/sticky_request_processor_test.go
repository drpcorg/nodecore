package flow_test

import (
	"context"
	"errors"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams/flow"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/drpcorg/dsheltie/pkg/test_utils"
	"github.com/drpcorg/dsheltie/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"testing"
)

func TestNotStickyRequestThenError(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("method", nil)
	processor := flow.NewStickyRequestProcessor(chains.POLYGON, upSupervisor)
	result := processor.ProcessRequest(context.Background(), nil, request)

	upSupervisor.AssertNotCalled(t, "GetChainSupervisor", mock.Anything)
	upSupervisor.AssertNotCalled(t, "GetExecutor")
	upSupervisor.AssertNotCalled(t, "GetUpstream", mock.Anything)

	expected := &protocol.ResponseHolderWrapper{
		UpstreamId: flow.NoUpstream,
		RequestId:  request.Id(),
		Response:   protocol.NewTotalFailureFromErr(request.Id(), protocol.ServerError(), request.RequestType()),
	}

	assert.IsType(t, &flow.UnaryResponse{}, result)
	assert.Equal(t, expected, result.(*flow.UnaryResponse).ResponseWrapper)
}

func TestStickySendNoNothingToParseThenRequestAsIs(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	specMethod := specs.MethodWithSettings("method", &specs.MethodSettings{Sticky: &specs.Sticky{SendSticky: true}}, nil)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("method", nil, specMethod)
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewConnectorMock()
	upstream := test_utils.TestUpstream(context.Background(), apiConnector, upConfig())
	result := []byte("result")
	responseHolder := protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc)
	processor := flow.NewStickyRequestProcessor(chains.POLYGON, upSupervisor)

	upSupervisor.On("GetExecutor").Return(createExecutor())
	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("SendRequest", mock.Anything, request).Return(responseHolder)

	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	upSupervisor.AssertExpectations(t)
	strategy.AssertExpectations(t)
	apiConnector.AssertExpectations(t)

	expected := &protocol.ResponseHolderWrapper{
		UpstreamId: "id",
		RequestId:  request.Id(),
		Response:   protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc),
	}

	assert.Equal(t, expected, response.(*flow.UnaryResponse).ResponseWrapper)
}

func TestStickySendModifyRequest(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	tagParser := &specs.TagParser{ReturnType: specs.StringType, Path: ".[1]"}
	specMethod := specs.MethodWithSettings("method", &specs.MethodSettings{Sticky: &specs.Sticky{SendSticky: true}}, tagParser)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("method", []any{12, "123456789"}, specMethod)
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewConnectorMock()
	upstream := test_utils.TestUpstream(context.Background(), apiConnector, upConfig())
	result := []byte("result")
	responseHolder := protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc)
	processor := flow.NewStickyRequestProcessor(chains.POLYGON, upSupervisor)

	upSupervisor.On("GetExecutor").Return(createExecutor())
	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("SendRequest", mock.Anything, request).Return(responseHolder)

	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	upSupervisor.AssertExpectations(t)
	strategy.AssertExpectations(t)
	apiConnector.AssertExpectations(t)

	expected := &protocol.ResponseHolderWrapper{
		UpstreamId: "id",
		RequestId:  request.Id(),
		Response:   protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc),
	}
	expectedBody := []byte(`{"id":"1","jsonrpc":"2.0","method":"method","params":[12,"1234"]}`)
	realBody, err := request.Body()

	assert.Nil(t, err)
	assert.Equal(t, expected, response.(*flow.UnaryResponse).ResponseWrapper)
	assert.Equal(t, expectedBody, realBody)
}

func TestStickyRequestError(t *testing.T) {
	tests := []struct {
		name       string
		specMethod *specs.Method
	}{
		{
			name:       "send-sticky",
			specMethod: specs.MethodWithSettings("method", &specs.MethodSettings{Sticky: &specs.Sticky{SendSticky: true}}, nil),
		},
		{
			name:       "create-sticky",
			specMethod: specs.MethodWithSettings("method", &specs.MethodSettings{Sticky: &specs.Sticky{CreateSticky: true}}, nil),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			upSupervisor := mocks.NewUpstreamSupervisorMock()
			request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("method", nil, test.specMethod)
			strategy := mocks.NewMockStrategy()
			err := errors.New("error")
			processor := flow.NewStickyRequestProcessor(chains.POLYGON, upSupervisor)

			upSupervisor.On("GetExecutor").Return(createExecutor())
			strategy.On("SelectUpstream", request).Return("", err)

			response := processor.ProcessRequest(context.Background(), strategy, request)
			expected := &protocol.ResponseHolderWrapper{
				UpstreamId: flow.NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
			}

			upSupervisor.AssertExpectations(t)
			strategy.AssertExpectations(t)

			assert.IsType(te, &flow.UnaryResponse{}, response)
			assert.Equal(te, expected, response.(*flow.UnaryResponse).ResponseWrapper)
		})
	}
}

func TestCreateStickyModifyResponse(t *testing.T) {
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	specMethod := specs.MethodWithSettings("method", &specs.MethodSettings{Sticky: &specs.Sticky{CreateSticky: true}}, nil)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("method", nil, specMethod)
	strategy := mocks.NewMockStrategy()
	apiConnector := mocks.NewConnectorMock()
	upstream := test_utils.TestUpstream(context.Background(), apiConnector, upConfig())
	result := []byte(`"result"`)
	responseHolder := protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc)
	processor := flow.NewStickyRequestProcessor(chains.POLYGON, upSupervisor)

	upSupervisor.On("GetExecutor").Return(createExecutor())
	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("SendRequest", mock.Anything, request).Return(responseHolder)

	response := processor.ProcessRequest(context.Background(), strategy, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	upSupervisor.AssertExpectations(t)
	strategy.AssertExpectations(t)
	apiConnector.AssertExpectations(t)

	expected := &protocol.ResponseHolderWrapper{
		UpstreamId: "id",
		RequestId:  request.Id(),
		Response:   protocol.NewSimpleHttpUpstreamResponse(request.Id(), []byte(`"result00012"`), request.RequestType()),
	}

	assert.Equal(t, expected, response.(*flow.UnaryResponse).ResponseWrapper)
}
