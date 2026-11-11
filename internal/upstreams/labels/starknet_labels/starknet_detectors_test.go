package starknet_labels_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/starknet_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func versionRequest(t *testing.T, method string) *protocol.UpstreamJsonRpcRequest {
	t.Helper()
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(method, []any{}, chains.STARKNET)
	require.NoError(t, err)
	return request
}

func methodNotFoundResponse() protocol.ResponseHolder {
	return protocol.NewHttpUpstreamResponse(
		"1",
		[]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method Not Found"}}`),
		200,
		protocol.JsonRpc,
	)
}

func starknetHandler(connector *mocks.ConnectorMock) *labels.ClientLabelsDetectorHandler {
	return labels.NewClientLabelDetectorHandler(
		"upstream-id",
		connector,
		time.Second,
		starknet_labels.NewPathfinderClientLabelsDetector(chains.STARKNET),
		starknet_labels.NewJunoClientLabelsDetector(chains.STARKNET),
	)
}

func TestStarknetDetectorsImplementClientLabelsDetector(t *testing.T) {
	var pathfinder labels.ClientLabelsDetector = starknet_labels.NewPathfinderClientLabelsDetector(chains.STARKNET)
	var juno labels.ClientLabelsDetector = starknet_labels.NewJunoClientLabelsDetector(chains.STARKNET)

	assert.NotNil(t, pathfinder)
	assert.NotNil(t, juno)
}

func TestStarknetHandlerPathfinderAnswers(t *testing.T) {
	pathfinderRequest := versionRequest(t, "pathfinder_version")
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(pathfinderRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"v0.16.4"`), protocol.JsonRpc)).
		Once()

	result := starknetHandler(connector).DetectLabels(context.Background())

	assert.Equal(t, map[string]string{"client_type": "pathfinder", "client_version": "0.16.4"}, result)
	connector.AssertNumberOfCalls(t, "SendRequest", 1)
}

func TestStarknetHandlerFallsBackToJuno(t *testing.T) {
	pathfinderRequest := versionRequest(t, "pathfinder_version")
	junoRequest := versionRequest(t, "juno_version")
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(pathfinderRequest))).
		Return(methodNotFoundResponse()).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(junoRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"v0.12.0"`), protocol.JsonRpc)).
		Once()

	result := starknetHandler(connector).DetectLabels(context.Background())

	assert.Equal(t, map[string]string{"client_type": "juno", "client_version": "0.12.0"}, result)
	connector.AssertExpectations(t)
}

func TestStarknetHandlerBothProbesFail(t *testing.T) {
	pathfinderRequest := versionRequest(t, "pathfinder_version")
	junoRequest := versionRequest(t, "juno_version")
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(pathfinderRequest))).
		Return(methodNotFoundResponse()).
		Times(2)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(junoRequest))).
		Return(methodNotFoundResponse()).
		Times(2)
	handler := starknetHandler(connector)

	assert.Nil(t, handler.DetectLabels(context.Background()))
	_, _, err := handler.DetectClient(context.Background())
	assert.Error(t, err)
	connector.AssertExpectations(t)
}

func TestStarknetHandlerGarbageResultKeepsClientType(t *testing.T) {
	pathfinderRequest := versionRequest(t, "pathfinder_version")
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(pathfinderRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"x":1}`), protocol.JsonRpc)).
		Once()

	result := starknetHandler(connector).DetectLabels(context.Background())

	assert.Equal(t, map[string]string{"client_type": "pathfinder"}, result)
	connector.AssertNumberOfCalls(t, "SendRequest", 1)
}

func TestStarknetHandlerEmptyVersionKeepsClientType(t *testing.T) {
	pathfinderRequest := versionRequest(t, "pathfinder_version")
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(pathfinderRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`""`), protocol.JsonRpc)).
		Once()

	result := starknetHandler(connector).DetectLabels(context.Background())

	assert.Equal(t, map[string]string{"client_type": "pathfinder"}, result)
	connector.AssertNumberOfCalls(t, "SendRequest", 1)
}
