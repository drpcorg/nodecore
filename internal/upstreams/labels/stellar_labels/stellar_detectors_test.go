package stellar_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/stellar_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStellarClientLabelsDetectorNodeTypeRequest(t *testing.T) {
	detector := stellar_labels.NewStellarClientLabelsDetector(chains.STELLAR)

	request, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	require.NotNil(t, request)

	assert.Equal(t, "getVersionInfo", request.Method())
	assert.Equal(t, protocol.JsonRpc, request.RequestType())

	body, err := request.Body()
	require.NoError(t, err)

	assert.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"getVersionInfo","params":{}}`, string(body))
}

func TestStellarClientLabelsDetectorClientVersionAndType(t *testing.T) {
	tests := []struct {
		name               string
		data               []byte
		expectedVersion    string
		expectedClientType string
		expectErr          bool
	}{
		{
			name:               "truncates version at first dash",
			data:               []byte(`{"version":"27.1.1-7e71bb47b8619b0e3e0a48979e6b048ce88fcd23","commitHash":"7e71bb47b8619b0e3e0a48979e6b048ce88fcd23","captiveCoreVersion":"stellar-core 27.1.0","protocolVersion":27}`),
			expectedVersion:    "27.1.1",
			expectedClientType: "stellar-rpc",
		},
		{
			name:               "keeps version without dash verbatim",
			data:               []byte(`{"version":"27.1.1","protocolVersion":27}`),
			expectedVersion:    "27.1.1",
			expectedClientType: "stellar-rpc",
		},
		{
			name:               "falls back on missing version",
			data:               []byte(`{"commitHash":"7e71bb47","protocolVersion":27}`),
			expectedVersion:    "",
			expectedClientType: "stellar-rpc",
		},
		{
			name:      "fails on invalid json",
			data:      []byte(`{"version":`),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := stellar_labels.NewStellarClientLabelsDetector(chains.STELLAR)

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
