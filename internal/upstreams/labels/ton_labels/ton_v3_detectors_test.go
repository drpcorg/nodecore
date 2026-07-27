package ton_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/ton_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTonV3ClientLabelsDetectorNodeTypeRequest(t *testing.T) {
	detector := ton_labels.NewTonV3ClientLabelsDetector(chains.TON)

	request, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	require.NotNil(t, request)

	assert.Equal(t, "GET#/doc.json", request.Method())
	assert.Equal(t, protocol.Rest, request.RequestType())
}

func TestTonV3ClientLabelsDetectorClientVersionAndType(t *testing.T) {
	tests := []struct {
		name               string
		data               []byte
		expectedVersion    string
		expectedClientType string
		expectErr          bool
	}{
		{
			name:               "parses go indexer title",
			data:               []byte(`{"info":{"title":"TON Index (Go)","version":"1.2.8"},"paths":{}}`),
			expectedVersion:    "1.2.8",
			expectedClientType: "ton-index-go",
		},
		{
			name:               "strips leading v",
			data:               []byte(`{"info":{"title":"TON Index (Go)","version":"v1.2.8"},"paths":{}}`),
			expectedVersion:    "1.2.8",
			expectedClientType: "ton-index-go",
		},
		{
			name:               "falls back on other title",
			data:               []byte(`{"info":{"title":"TON Index API","version":"1.0.0"},"paths":{}}`),
			expectedVersion:    "1.0.0",
			expectedClientType: "ton-index",
		},
		{
			name:               "falls back on missing info",
			data:               []byte(`{"openapi":"3.0.2","paths":{}}`),
			expectedVersion:    "",
			expectedClientType: "ton-index",
		},
		{
			name:      "fails on invalid json",
			data:      []byte(`{"info":`),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := ton_labels.NewTonV3ClientLabelsDetector(chains.TON)

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
