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

func TestStarknetLabelsDetectorImplementsLabelsDetector(t *testing.T) {
	detector := starknet_labels.NewStarknetLabelsDetector("upstream-id", mocks.NewConnectorMock(), chains.STARKNET, time.Second)

	require.NotNil(t, detector)

	var labelsDetector labels.LabelsDetector = detector
	assert.NotNil(t, labelsDetector)
}

func TestStarknetLabelsDetectorPathfinderAnswers(t *testing.T) {
	pathfinderRequest := versionRequest(t, "pathfinder_version")

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(pathfinderRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0.16.4"`), protocol.JsonRpc)).
		Once()

	detector := starknet_labels.NewStarknetLabelsDetector("upstream-id", connector, chains.STARKNET, time.Second)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{
		"client_type":    "pathfinder",
		"client_version": "0.16.4",
	}, result)
	connector.AssertExpectations(t)
	connector.AssertNumberOfCalls(t, "SendRequest", 1)
}

func TestStarknetLabelsDetectorFallsBackToJunoAndStripsLeadingV(t *testing.T) {
	pathfinderRequest := versionRequest(t, "pathfinder_version")
	junoRequest := versionRequest(t, "juno_version")

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(pathfinderRequest))).
		Return(methodNotFoundResponse()).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(junoRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"v0.16.4"`), protocol.JsonRpc)).
		Once()

	detector := starknet_labels.NewStarknetLabelsDetector("upstream-id", connector, chains.STARKNET, time.Second)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{
		"client_type":    "juno",
		"client_version": "0.16.4",
	}, result)
	connector.AssertExpectations(t)
}

func TestStarknetLabelsDetectorBothProbesFail(t *testing.T) {
	pathfinderRequest := versionRequest(t, "pathfinder_version")
	junoRequest := versionRequest(t, "juno_version")

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(pathfinderRequest))).
		Return(methodNotFoundResponse()).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(junoRequest))).
		Return(protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.TotalFailure)).
		Once()

	detector := starknet_labels.NewStarknetLabelsDetector("upstream-id", connector, chains.STARKNET, time.Second)

	result := detector.DetectLabels()

	assert.Nil(t, result)
	connector.AssertExpectations(t)
}

func TestStarknetLabelsDetectorGarbageResultEmitsOnlyClientType(t *testing.T) {
	pathfinderRequest := versionRequest(t, "pathfinder_version")

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(pathfinderRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"version":"0.16.4"}`), protocol.JsonRpc)).
		Once()

	detector := starknet_labels.NewStarknetLabelsDetector("upstream-id", connector, chains.STARKNET, time.Second)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{"client_type": "pathfinder"}, result)
	connector.AssertExpectations(t)
	connector.AssertNumberOfCalls(t, "SendRequest", 1)
}

func TestStarknetLabelsDetectorEmptyVersionEmitsOnlyClientType(t *testing.T) {
	pathfinderRequest := versionRequest(t, "pathfinder_version")

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(pathfinderRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`""`), protocol.JsonRpc)).
		Once()

	detector := starknet_labels.NewStarknetLabelsDetector("upstream-id", connector, chains.STARKNET, time.Second)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{"client_type": "pathfinder"}, result)
	connector.AssertExpectations(t)
}

func TestStarknetLabelsDetectorUsesConfiguredTimeout(t *testing.T) {
	pathfinderRequest := versionRequest(t, "pathfinder_version")
	timeout := 50 * time.Millisecond

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.MatchedBy(func(ctx context.Context) bool {
			deadline, ok := ctx.Deadline()
			if !ok {
				return false
			}
			remaining := time.Until(deadline)
			return remaining > 0 && remaining <= timeout
		}), mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(pathfinderRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0.16.4"`), protocol.JsonRpc)).
		Once()

	detector := starknet_labels.NewStarknetLabelsDetector("upstream-id", connector, chains.STARKNET, timeout)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{
		"client_type":    "pathfinder",
		"client_version": "0.16.4",
	}, result)
	connector.AssertExpectations(t)
}
