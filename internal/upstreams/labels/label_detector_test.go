package labels_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewClientLabelDetectorHandlerImplementsLabelsDetector(t *testing.T) {
	handler := labels.NewClientLabelDetectorHandler("upstream-id", mocks.NewConnectorMock(), mocks.NewClientLabelsDetectorMock(), time.Second)

	require.NotNil(t, handler)

	var detector labels.LabelsDetector = handler
	assert.NotNil(t, detector)
}

func TestClientLabelsDetectorHandlerDetectLabelsReturnsNilWhenNodeTypeRequestFails(t *testing.T) {
	clientDetector := mocks.NewClientLabelsDetectorMock()
	clientDetector.On("NodeTypeRequest").Return(nil, errors.New("request failed")).Once()

	connector := mocks.NewConnectorMock()

	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, clientDetector, time.Second)

	result := handler.DetectLabels()

	assert.Nil(t, result)
	clientDetector.AssertExpectations(t)
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

func TestClientLabelsDetectorHandlerDetectLabelsReturnsNilWhenConnectorReturnsError(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getVersion", nil, chains.SOLANA)
	require.NoError(t, err)

	clientDetector := mocks.NewClientLabelsDetectorMock()
	clientDetector.On("NodeTypeRequest").Return(request, nil).Once()

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.TotalFailure)).
		Once()

	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, clientDetector, time.Second)

	result := handler.DetectLabels()

	assert.Nil(t, result)
	clientDetector.AssertExpectations(t)
	clientDetector.AssertNotCalled(t, "ClientVersionAndType", mock.Anything)
	connector.AssertExpectations(t)
}

func TestClientLabelsDetectorHandlerDetectLabelsReturnsNilWhenClientVersionParsingFails(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getVersion", nil, chains.SOLANA)
	require.NoError(t, err)

	responseBody := []byte(`{"solana-core":"1.18.23"}`)

	clientDetector := mocks.NewClientLabelsDetectorMock()
	clientDetector.On("NodeTypeRequest").Return(request, nil).Once()
	clientDetector.On("ClientVersionAndType", responseBody).Return("", "", errors.New("parse failed")).Once()

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", responseBody, protocol.JsonRpc)).
		Once()

	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, clientDetector, time.Second)

	result := handler.DetectLabels()

	assert.Nil(t, result)
	clientDetector.AssertExpectations(t)
	connector.AssertExpectations(t)
}

func TestClientLabelsDetectorHandlerDetectLabelsReturnsBothLabels(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getVersion", nil, chains.SOLANA)
	require.NoError(t, err)

	responseBody := []byte(`{"solana-core":"1.18.23"}`)

	clientDetector := mocks.NewClientLabelsDetectorMock()
	clientDetector.On("NodeTypeRequest").Return(request, nil).Once()
	clientDetector.On("ClientVersionAndType", responseBody).Return("1.18.23", "solana", nil).Once()

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", responseBody, protocol.JsonRpc)).
		Once()

	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, clientDetector, time.Second)

	result := handler.DetectLabels()

	assert.Equal(t, map[string]string{
		"client_version": "1.18.23",
		"client_type":    "solana",
	}, result)
	clientDetector.AssertExpectations(t)
	connector.AssertExpectations(t)
}

func TestClientLabelsDetectorHandlerDetectLabelsReturnsOnlyClientVersionWhenTypeIsEmpty(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getVersion", nil, chains.SOLANA)
	require.NoError(t, err)

	responseBody := []byte(`{"solana-core":"1.18.23"}`)

	clientDetector := mocks.NewClientLabelsDetectorMock()
	clientDetector.On("NodeTypeRequest").Return(request, nil).Once()
	clientDetector.On("ClientVersionAndType", responseBody).Return("1.18.23", "", nil).Once()

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", responseBody, protocol.JsonRpc)).
		Once()

	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, clientDetector, time.Second)

	result := handler.DetectLabels()

	assert.Equal(t, map[string]string{
		"client_version": "1.18.23",
	}, result)
	clientDetector.AssertExpectations(t)
	connector.AssertExpectations(t)
}

func TestClientLabelsDetectorHandlerDetectLabelsReturnsOnlyClientTypeWhenVersionIsEmpty(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getVersion", nil, chains.SOLANA)
	require.NoError(t, err)

	responseBody := []byte(`{"solana-core":"1.18.23"}`)

	clientDetector := mocks.NewClientLabelsDetectorMock()
	clientDetector.On("NodeTypeRequest").Return(request, nil).Once()
	clientDetector.On("ClientVersionAndType", responseBody).Return("", "solana", nil).Once()

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", responseBody, protocol.JsonRpc)).
		Once()

	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, clientDetector, time.Second)

	result := handler.DetectLabels()

	assert.Equal(t, map[string]string{
		"client_type": "solana",
	}, result)
	clientDetector.AssertExpectations(t)
	connector.AssertExpectations(t)
}

func TestClientLabelsDetectorHandlerDetectLabelsReturnsEmptyMapWhenAllLabelsAreEmpty(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getVersion", nil, chains.SOLANA)
	require.NoError(t, err)

	responseBody := []byte(`{"solana-core":"1.18.23"}`)

	clientDetector := mocks.NewClientLabelsDetectorMock()
	clientDetector.On("NodeTypeRequest").Return(request, nil).Once()
	clientDetector.On("ClientVersionAndType", responseBody).Return("", "", nil).Once()

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", responseBody, protocol.JsonRpc)).
		Once()

	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, clientDetector, time.Second)

	result := handler.DetectLabels()

	assert.Empty(t, result)
	require.NotNil(t, result)
	clientDetector.AssertExpectations(t)
	connector.AssertExpectations(t)
}

func TestClientLabelsDetectorHandlerDetectLabelsUsesConfiguredTimeout(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getVersion", nil, chains.SOLANA)
	require.NoError(t, err)

	responseBody := []byte(`{"solana-core":"1.18.23"}`)
	timeout := 50 * time.Millisecond

	clientDetector := mocks.NewClientLabelsDetectorMock()
	clientDetector.On("NodeTypeRequest").Return(request, nil).Once()
	clientDetector.On("ClientVersionAndType", responseBody).Return("1.18.23", "solana", nil).Once()

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.MatchedBy(func(ctx context.Context) bool {
			deadline, ok := ctx.Deadline()
			if !ok {
				return false
			}

			remaining := time.Until(deadline)
			return remaining > 0 && remaining <= timeout
		}), mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", responseBody, protocol.JsonRpc)).
		Once()

	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, clientDetector, timeout)

	result := handler.DetectLabels()

	assert.Equal(t, map[string]string{
		"client_version": "1.18.23",
		"client_type":    "solana",
	}, result)
	clientDetector.AssertExpectations(t)
	connector.AssertExpectations(t)
}
