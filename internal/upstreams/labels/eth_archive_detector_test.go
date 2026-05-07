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

func TestEthArchiveLabelsDetectorDetectLabelsReturnsArchiveLabel(t *testing.T) {
	tests := []struct {
		name          string
		chain         chains.Chain
		expectedBlock string
	}{
		{
			name:          "uses default earliest block",
			chain:         chains.ETHEREUM,
			expectedBlock: labels.EarliestBlock,
		},
		{
			name:          "uses arbitrum nitro block",
			chain:         chains.ARBITRUM,
			expectedBlock: labels.ArbitrumNitroBlock,
		},
		{
			name:          "uses optimism bedrock block",
			chain:         chains.OPTIMISM,
			expectedBlock: labels.OptimismBedrockBlock,
		},
		{
			name:          "uses evmos genesis block",
			chain:         chains.EVMOS,
			expectedBlock: labels.EvmosGenesisBlock,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := protocol.NewInternalUpstreamJsonRpcRequest(
				"eth_getBalance",
				[]any{"0x0000000000000000000000000000000000000000", tt.expectedBlock},
				tt.chain,
			)
			require.NoError(t, err)

			connector := mocks.NewConnectorMock()
			connector.
				On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
				Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x0"`), protocol.JsonRpc)).
				Once()

			detector := labels.NewEthArchiveLabelsDetector("upstream-id", tt.chain, time.Second, connector)

			result := detector.DetectLabels()

			assert.Equal(t, map[string]string{
				"archive": "true",
			}, result)
			connector.AssertExpectations(t)
		})
	}
}

func TestEthArchiveLabelsDetectorDetectLabelsReturnsFalseWhenConnectorReturnsError(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_getBalance",
		[]any{"0x0000000000000000000000000000000000000000", labels.EarliestBlock},
		chains.ETHEREUM,
	)
	require.NoError(t, err)

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.TotalFailure)).
		Once()

	detector := labels.NewEthArchiveLabelsDetector("upstream-id", chains.ETHEREUM, time.Second, connector)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{
		"archive": "false",
	}, result)
	connector.AssertExpectations(t)
}
