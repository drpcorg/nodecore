package labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSolanaClientLabelsDetectorImplementsClientLabelsDetector(t *testing.T) {
	detector := labels.NewSolanaClientLabelsDetector()

	require.NotNil(t, detector)

	var clientDetector labels.ClientLabelsDetector = detector
	assert.NotNil(t, clientDetector)
}

func TestSolanaClientLabelsDetectorNodeTypeRequest(t *testing.T) {
	detector := labels.NewSolanaClientLabelsDetector()

	request, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	require.NotNil(t, request)

	assert.Equal(t, "1", request.Id())
	assert.Equal(t, "getVersion", request.Method())
	assert.Nil(t, request.Headers())
	assert.Equal(t, protocol.JsonRpc, request.RequestType())
	assert.False(t, request.IsStream())
	assert.False(t, request.IsSubscribe())
	assert.Empty(t, request.RequestHash())
	assert.NotNil(t, request.RequestObserver())
	assert.Equal(t, protocol.InternalUnary, request.RequestObserver().GetRequestKind())

	body, err := request.Body()
	require.NoError(t, err)

	assert.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"getVersion","params":null}`, string(body))
}

func TestSolanaClientLabelsDetectorClientVersionAndType(t *testing.T) {
	tests := []struct {
		name               string
		data               []byte
		expectedVersion    string
		expectedClientType string
		expectErr          bool
	}{
		{
			name:               "parses valid payload",
			data:               []byte(`{"solana-core":"1.18.23"}`),
			expectedVersion:    "1.18.23",
			expectedClientType: "solana",
		},
		{
			name:               "ignores unknown fields",
			data:               []byte(`{"solana-core":"2.0.1","feature-set":123}`),
			expectedVersion:    "2.0.1",
			expectedClientType: "solana",
		},
		{
			name:               "returns empty version when field is missing",
			data:               []byte(`{"other":"value"}`),
			expectedVersion:    "",
			expectedClientType: "solana",
		},
		{
			name:               "returns empty version for empty object",
			data:               []byte(`{}`),
			expectedVersion:    "",
			expectedClientType: "solana",
		},
		{
			name:      "fails on invalid json",
			data:      []byte(`{"solana-core":`),
			expectErr: true,
		},
		{
			name:      "fails on wrong field type",
			data:      []byte(`{"solana-core":123}`),
			expectErr: true,
		},
	}

	detector := labels.NewSolanaClientLabelsDetector()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
