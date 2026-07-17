package ripple_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/ripple_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRippleClientLabelsDetectorNodeTypeRequest(t *testing.T) {
	detector := ripple_labels.NewRippleClientLabelsDetector(chains.RIPPLE)

	request, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	require.NotNil(t, request)

	assert.Equal(t, "server_info", request.Method())
	assert.Equal(t, protocol.JsonRpc, request.RequestType())

	body, err := request.Body()
	require.NoError(t, err)

	assert.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"server_info","params":[{}]}`, string(body))
}

func TestRippleClientLabelsDetectorClientVersionAndType(t *testing.T) {
	tests := []struct {
		name               string
		data               []byte
		expectedVersion    string
		expectedClientType string
		expectErr          bool
	}{
		{
			name:               "parses rippled version",
			data:               []byte(`{"info":{"build_version":"3.2.0","complete_ledgers":"32570-100000000"},"status":"success"}`),
			expectedVersion:    "3.2.0",
			expectedClientType: "rippled",
		},
		{
			name:               "parses clio version",
			data:               []byte(`{"info":{"clio_version":"2.5.0","rippled":{"build_version":"3.2.0"}}}`),
			expectedVersion:    "2.5.0",
			expectedClientType: "clio",
		},
		{
			name:               "falls back on missing versions",
			data:               []byte(`{"info":{"complete_ledgers":"32570-100000000"}}`),
			expectedVersion:    "",
			expectedClientType: "rippled",
		},
		{
			name:      "fails on invalid json",
			data:      []byte(`{"info":`),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := ripple_labels.NewRippleClientLabelsDetector(chains.RIPPLE)

			version, clientType, err := detector.ClientVersionAndType(tt.data)

			if tt.expectErr {
				require.Error(t, err)
				assert.Empty(t, version)
				assert.Empty(t, clientType)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedVersion, version)
			assert.Equal(t, tt.expectedClientType, clientType)
		})
	}
}
