package ton_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/ton_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTonClientLabelsDetectorNodeTypeRequest(t *testing.T) {
	detector := ton_labels.NewTonClientLabelsDetector(chains.TON)

	request, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	require.NotNil(t, request)

	assert.Equal(t, "GET#/openapi.json", request.Method())
	assert.Equal(t, protocol.Rest, request.RequestType())
}

func TestTonClientLabelsDetectorClientVersionAndType(t *testing.T) {
	tests := []struct {
		name               string
		data               []byte
		expectedVersion    string
		expectedClientType string
		expectErr          bool
	}{
		{
			name:               "parses cpp title and strips leading v",
			data:               []byte(`{"openapi":"3.1.0","info":{"title":"TON HTTP API C++","version":"v2.1.13-9bae630"},"paths":{}}`),
			expectedVersion:    "2.1.13-9bae630",
			expectedClientType: "ton-http-api-cpp",
		},
		{
			name:               "parses python title",
			data:               []byte(`{"openapi":"3.0.2","info":{"title":"TON HTTP API","version":"2.0.36"},"paths":{}}`),
			expectedVersion:    "2.0.36",
			expectedClientType: "ton-http-api",
		},
		{
			name:               "falls back on missing info",
			data:               []byte(`{"openapi":"3.0.2","paths":{}}`),
			expectedVersion:    "",
			expectedClientType: "ton-http-api",
		},
		{
			name:               "falls back on unknown title",
			data:               []byte(`{"info":{"title":"Some Other API","version":"1.0.0"}}`),
			expectedVersion:    "1.0.0",
			expectedClientType: "ton-http-api",
		},
		{
			name:      "fails on invalid json",
			data:      []byte(`{"info":`),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := ton_labels.NewTonClientLabelsDetector(chains.TON)

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
