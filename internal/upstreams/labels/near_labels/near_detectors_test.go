package near_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/near_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNearClientLabelsDetectorNodeTypeRequest(t *testing.T) {
	detector := near_labels.NewNearClientLabelsDetector(chains.NEAR)

	request, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	require.NotNil(t, request)

	assert.Equal(t, "status", request.Method())
	assert.Equal(t, protocol.JsonRpc, request.RequestType())

	body, err := request.Body()
	require.NoError(t, err)

	assert.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"status","params":[]}`, string(body))
}

func TestNearClientLabelsDetectorClientVersionAndType(t *testing.T) {
	tests := []struct {
		name               string
		data               []byte
		expectedVersion    string
		expectedClientType string
		expectErr          bool
	}{
		{
			name:               "parses neard version",
			data:               []byte(`{"version":{"version":"2.13.1","build":"unknown","rustc_version":"1.93.0"},"chain_id":"mainnet"}`),
			expectedVersion:    "2.13.1",
			expectedClientType: "neard",
		},
		{
			name:               "falls back on missing version",
			data:               []byte(`{"chain_id":"mainnet"}`),
			expectedVersion:    "",
			expectedClientType: "neard",
		},
		{
			name:               "falls back on empty version",
			data:               []byte(`{"version":{"version":"","build":"unknown"}}`),
			expectedVersion:    "",
			expectedClientType: "neard",
		},
		{
			name:      "fails on invalid json",
			data:      []byte(`{"version":`),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := near_labels.NewNearClientLabelsDetector(chains.NEAR)

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
