package eth_labels_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/eth_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func historicalProofsRequest(t *testing.T) protocol.RequestHolder {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("debug_proofsSyncStatus", []any{}, chains.OPTIMISM)
	require.NoError(t, err)
	return request
}

func TestEthHistoricalProofsLabelsDetector(t *testing.T) {
	tests := []struct {
		name     string
		response protocol.ResponseHolder
		expected map[string]string
	}{
		{"window answered", protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"earliest":"0x64","latest":"0xc8"}`), protocol.JsonRpc), map[string]string{eth_labels.HistoricalProofsLabel: "true"}},
		{"empty store still has a proof store", protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"earliest":"0x0","latest":"0x0"}`), protocol.JsonRpc), map[string]string{eth_labels.HistoricalProofsLabel: "true"}},
		{"method absent", protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("debug_proofsSyncStatus")), map[string]string{eth_labels.HistoricalProofsLabel: "false"}},
		{"transient error keeps last verdict", protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom")), nil},
		{"malformed keeps last verdict", protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"earliest":"0x64"}`), protocol.JsonRpc), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			connector.
				On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(historicalProofsRequest(t)))).
				Return(tt.response).
				Once()
			detector := eth_labels.NewEthHistoricalProofsLabelsDetector("id", chains.OPTIMISM, time.Second, connector)

			assert.Equal(t, tt.expected, detector.DetectLabels())
			connector.AssertExpectations(t)
		})
	}
}
