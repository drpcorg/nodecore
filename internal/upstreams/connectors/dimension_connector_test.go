package connectors_test

import (
	"context"
	"errors"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/dimensions"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams/connectors"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"testing"
	"time"
)

func TestDimensionConnectorSuccessfulResponse(t *testing.T) {
	tracker := dimensions.NewDimensionTracker()
	executor := protocol.CreateUpstreamExecutor()
	connectorMock := mocks.NewConnectorMock()
	dimensionConnector := connectors.NewDimensionTrackerConnector(chains.ARBITRUM, "id", connectorMock, tracker, executor)

	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
	responseHolder := protocol.NewSimpleHttpUpstreamResponse("1", []byte("res"), protocol.JsonRpc)
	connectorMock.On("SendRequest", mock.Anything, request).Return(responseHolder)

	result := dimensionConnector.SendRequest(context.Background(), request)
	dims := tracker.GetUpstreamDimensions(chains.ARBITRUM, "id", "eth_call")

	connectorMock.AssertExpectations(t)

	assert.Equal(t, responseHolder, result)
	assert.Equal(t, uint64(1), dims.GetTotalRequests())
	assert.Equal(t, uint64(0), dims.GetTotalErrors())
	assert.Equal(t, float64(0), dims.GetErrorRate())
	assert.Equal(t, uint64(0), dims.GetSuccessfulRetries())
	assert.True(t, dims.GetValueAtQuantile(0.9) > 0)
}

func TestDimensionConnectorRetryRequest(t *testing.T) {
	tracker := dimensions.NewDimensionTracker()
	executor := protocol.CreateUpstreamExecutor(
		protocol.CreateUpstreamRetryPolicy(&config.RetryConfig{Attempts: 3, Delay: 3 * time.Millisecond}),
	)
	connectorMock := mocks.NewConnectorMock()
	dimensionConnector := connectors.NewDimensionTrackerConnector(chains.ARBITRUM, "id", connectorMock, tracker, executor)

	request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
	responseHolder := protocol.NewSimpleHttpUpstreamResponse("1", []byte("res"), protocol.JsonRpc)
	connectorMock.On("SendRequest", mock.Anything, request).Return(protocol.NewReplyError("1", protocol.ServerError(), protocol.JsonRpc, protocol.PartialFailure)).Once()
	connectorMock.On("SendRequest", mock.Anything, request).Return(responseHolder).Once()

	result := dimensionConnector.SendRequest(context.Background(), request)
	dims := tracker.GetUpstreamDimensions(chains.ARBITRUM, "id", "eth_call")

	connectorMock.AssertExpectations(t)

	assert.Equal(t, responseHolder, result)
	assert.Equal(t, uint64(2), dims.GetTotalRequests())
	assert.Equal(t, uint64(1), dims.GetTotalErrors())
	assert.Equal(t, 0.5, dims.GetErrorRate())
	assert.Equal(t, uint64(1), dims.GetSuccessfulRetries())
	assert.True(t, dims.GetValueAtQuantile(0.9) > 0)
}

func TestDimensionConnectorTypeTheSameAsDelegate(t *testing.T) {
	tests := []struct {
		name          string
		connectorType protocol.ApiConnectorType
	}{
		{
			name:          "json-rpc type",
			connectorType: protocol.JsonRpcConnector,
		},
		{
			name:          "ws type",
			connectorType: protocol.WsConnector,
		},
		{
			name:          "rest type",
			connectorType: protocol.RestConnector,
		},
		{
			name:          "grpc type",
			connectorType: protocol.GrpcConnector,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			connectorMock := mocks.NewConnectorMockWithType(test.connectorType)
			dimensionConnector := connectors.NewDimensionTrackerConnector(chains.ARBITRUM, "id", connectorMock, nil, nil)

			assert.Equal(te, test.connectorType, dimensionConnector.GetType())
		})
	}
}

func TestDimensionConnectorRetryableNonRetryableErrors(t *testing.T) {
	tests := []struct {
		name          string
		expectedError uint64
		errorResponse protocol.ResponseHolder
	}{
		{
			name:          "retryable error",
			expectedError: 1,
			errorResponse: protocol.NewReplyError("1", protocol.ServerError(), protocol.JsonRpc, protocol.PartialFailure),
		},
		{
			name:          "non retryable error",
			expectedError: 0,
			errorResponse: protocol.NewReplyError("1", protocol.CtxError(errors.New("err")), protocol.JsonRpc, protocol.TotalFailure),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			tracker := dimensions.NewDimensionTracker()
			executor := protocol.CreateUpstreamExecutor()
			connectorMock := mocks.NewConnectorMock()
			dimensionConnector := connectors.NewDimensionTrackerConnector(chains.ARBITRUM, "id", connectorMock, tracker, executor)

			request, _ := protocol.NewSimpleJsonRpcUpstreamRequest("223", []byte(`1`), "eth_call", nil, false)
			connectorMock.On("SendRequest", mock.Anything, request).Return(test.errorResponse)

			result := dimensionConnector.SendRequest(context.Background(), request)
			dims := tracker.GetUpstreamDimensions(chains.ARBITRUM, "id", "eth_call")

			connectorMock.AssertExpectations(te)

			assert.Equal(te, test.errorResponse, result)
			assert.Equal(te, uint64(1), dims.GetTotalRequests())
			assert.Equal(te, test.expectedError, dims.GetTotalErrors())
			assert.Equal(te, float64(test.expectedError), dims.GetErrorRate())
			assert.Equal(te, uint64(0), dims.GetSuccessfulRetries())
			assert.True(te, dims.GetValueAtQuantile(0.9) > 0)
		})
	}
}
