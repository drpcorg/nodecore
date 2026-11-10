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

func clientProbe(t *testing.T, method string, version, clientType string) (*mocks.ClientLabelsDetectorMock, protocol.RequestHolder) {
	t.Helper()
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(method, nil, chains.ETHEREUM)
	require.NoError(t, err)
	probe := mocks.NewClientLabelsDetectorMock()
	probe.On("NodeTypeRequest").Return(request, nil)
	probe.On("ClientVersionAndType", mock.Anything).Return(version, clientType, nil)
	return probe, request
}

func TestDetectClientReturnsTypeAndVersion(t *testing.T) {
	probe, request := clientProbe(t, "web3_clientVersion", "v1.13.14", "geth")
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"geth/v1.13.14/linux"`), protocol.JsonRpc)).
		Once()
	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, time.Second, probe)

	clientType, clientVersion, err := handler.DetectClient(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "geth", clientType)
	assert.Equal(t, "v1.13.14", clientVersion)
	connector.AssertExpectations(t)
}

func TestDetectClientReturnsErrorOnErrorAnswer(t *testing.T) {
	probe, request := clientProbe(t, "web3_clientVersion", "v1.13.14", "geth")
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).
		Once()
	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, time.Second, probe)

	_, _, err := handler.DetectClient(context.Background())

	assert.Error(t, err)
	probe.AssertNotCalled(t, "ClientVersionAndType", mock.Anything)
	connector.AssertExpectations(t)
}

func TestDetectClientReturnsErrorOnRequestBuildFailure(t *testing.T) {
	probe := mocks.NewClientLabelsDetectorMock()
	probe.On("NodeTypeRequest").Return(nil, errors.New("request failed")).Once()
	connector := mocks.NewConnectorMock()
	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, time.Second, probe)

	_, _, err := handler.DetectClient(context.Background())

	assert.EqualError(t, err, "request failed")
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

func TestDetectClientFallsThroughToSecondProbe(t *testing.T) {
	first, firstRequest := clientProbe(t, "pathfinder_version", "", "pathfinder")
	second, secondRequest := clientProbe(t, "juno_version", "0.12.0", "juno")
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(firstRequest))).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(secondRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"v0.12.0"`), protocol.JsonRpc)).
		Once()
	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, time.Second, first, second)

	clientType, clientVersion, err := handler.DetectClient(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "juno", clientType)
	assert.Equal(t, "0.12.0", clientVersion)
	first.AssertNotCalled(t, "ClientVersionAndType", mock.Anything)
	connector.AssertExpectations(t)
}

func TestDetectClientReturnsLastErrorWhenAllProbesFail(t *testing.T) {
	first, firstRequest := clientProbe(t, "pathfinder_version", "", "pathfinder")
	second, secondRequest := clientProbe(t, "juno_version", "", "juno")
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(firstRequest))).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(secondRequest))).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).
		Once()
	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, time.Second, first, second)

	_, _, err := handler.DetectClient(context.Background())

	assert.Error(t, err)
	connector.AssertNumberOfCalls(t, "SendRequest", 2)
}

func TestDetectClientUsesConfiguredTimeoutPerProbe(t *testing.T) {
	timeout := 50 * time.Millisecond
	probe, request := clientProbe(t, "web3_clientVersion", "v1.13.14", "geth")
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
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"geth/v1.13.14/linux"`), protocol.JsonRpc)).
		Once()
	handler := labels.NewClientLabelDetectorHandler("upstream-id", connector, timeout, probe)

	clientType, _, err := handler.DetectClient(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "geth", clientType)
	connector.AssertExpectations(t)
}
