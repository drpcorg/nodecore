package labels_test

import (
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

func TestEthFlashBlockDetectorDetectLabels(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_getBlockByNumber",
		[]any{"pending", false},
		chains.ETHEREUM,
	)
	require.NoError(t, err)

	tests := []struct {
		name     string
		response protocol.ResponseHolder
		expected map[string]string
	}{
		{
			name: "returns true when pending block has zero state root",
			response: protocol.NewSimpleHttpUpstreamResponse(
				"1",
				[]byte(`{"stateRoot":"0x0000000000000000000000000000000000000000000000000000000000000000"}`),
				protocol.JsonRpc,
			),
			expected: map[string]string{
				"flashblocks": "true",
			},
		},
		{
			name: "returns false when pending block has a non-zero state root",
			response: protocol.NewSimpleHttpUpstreamResponse(
				"1",
				[]byte(`{"stateRoot":"0x1234"}`),
				protocol.JsonRpc,
			),
			expected: map[string]string{
				"flashblocks": "false",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			connector.
				On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
				Return(tt.response).
				Once()

			detector := labels.NewEthFlashBlockDetector("upstream-id", chains.ETHEREUM, time.Second, connector)

			result := detector.DetectLabels()

			assert.Equal(t, tt.expected, result)
			connector.AssertExpectations(t)
		})
	}
}

func TestEthFlashBlockDetectorDetectLabelsReturnsNilWhenConnectorReturnsError(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_getBlockByNumber",
		[]any{"pending", false},
		chains.ETHEREUM,
	)
	require.NoError(t, err)

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.TotalFailure)).
		Once()

	detector := labels.NewEthFlashBlockDetector("upstream-id", chains.ETHEREUM, time.Second, connector)

	result := detector.DetectLabels()

	assert.Nil(t, result)
	connector.AssertExpectations(t)
}

func TestEthFlashBlockDetectorDetectLabelsReturnsNilWhenResponseIsInvalidJson(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_getBlockByNumber",
		[]any{"pending", false},
		chains.ETHEREUM,
	)
	require.NoError(t, err)

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"stateRoot":"0x0"`), protocol.JsonRpc)).
		Once()

	detector := labels.NewEthFlashBlockDetector("upstream-id", chains.ETHEREUM, time.Second, connector)

	result := detector.DetectLabels()

	assert.Nil(t, result)
	connector.AssertExpectations(t)
}
