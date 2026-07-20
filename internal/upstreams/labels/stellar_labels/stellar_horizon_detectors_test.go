package stellar_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/stellar_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStellarHorizonClientLabelsDetectorNodeTypeRequest(t *testing.T) {
	detector := stellar_labels.NewStellarHorizonClientLabelsDetector(chains.STELLAR)

	request, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	require.NotNil(t, request)

	assert.Equal(t, "GET#/", request.Method())
	assert.Equal(t, protocol.Rest, request.RequestType())
}

func TestStellarHorizonClientLabelsDetectorClientVersionAndType(t *testing.T) {
	tests := []struct {
		name               string
		data               []byte
		expectedVersion    string
		expectedClientType string
		expectErr          bool
	}{
		{
			name:               "truncates commit suffix",
			data:               []byte(`{"horizon_version":"27.0.0-a5df6aaa4b2b5e21e5a9d3e77d0344e57197b1d6","network_passphrase":"Public Global Stellar Network ; September 2015"}`),
			expectedVersion:    "27.0.0",
			expectedClientType: "horizon",
		},
		{
			name:               "keeps plain semver as is",
			data:               []byte(`{"horizon_version":"27.0.0"}`),
			expectedVersion:    "27.0.0",
			expectedClientType: "horizon",
		},
		{
			name:               "falls back on missing horizon_version",
			data:               []byte(`{"network_passphrase":"Public Global Stellar Network ; September 2015"}`),
			expectedVersion:    "",
			expectedClientType: "horizon",
		},
		{
			name:      "fails on invalid json",
			data:      []byte(`{"horizon_version":`),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := stellar_labels.NewStellarHorizonClientLabelsDetector(chains.STELLAR)

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
