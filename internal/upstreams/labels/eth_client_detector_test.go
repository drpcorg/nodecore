package labels_test

import (
	"errors"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEthClientLabelsDetectorNodeTypeRequest(t *testing.T) {
	detector := labels.NewEthClientLabelsDetector("upstream-id", chains.ETHEREUM, labels.EthMappingFunc)

	request, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	require.NotNil(t, request)

	assert.Equal(t, "1", request.Id())
	assert.Equal(t, "web3_clientVersion", request.Method())
	assert.Nil(t, request.Headers())
	assert.Equal(t, protocol.JsonRpc, request.RequestType())
	assert.False(t, request.IsStream())
	assert.False(t, request.IsSubscribe())
	assert.Empty(t, request.RequestHash())
	assert.NotNil(t, request.RequestObserver())
	assert.Equal(t, protocol.InternalUnary, request.RequestObserver().GetRequestKind())

	body, err := request.Body()
	require.NoError(t, err)

	assert.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"web3_clientVersion","params":null}`, string(body))
}

func TestEthMappingFunc(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		expected  string
		expectErr bool
	}{
		{
			name:     "parses string payload",
			data:     []byte(`"geth/v1.14.11-stable/linux-amd64/go1.22.1"`),
			expected: "geth/v1.14.11-stable/linux-amd64/go1.22.1",
		},
		{
			name:      "fails on invalid json",
			data:      []byte(`"geth/v1.14.11`),
			expectErr: true,
		},
		{
			name:      "fails on non-string payload",
			data:      []byte(`{"client":"geth"}`),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := labels.EthMappingFunc(tt.data)

			if tt.expectErr {
				require.Error(t, err)
				assert.Empty(t, client)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, client)
		})
	}
}

func TestEthClientLabelsDetectorClientVersionAndType(t *testing.T) {
	tests := []struct {
		name               string
		raw                string
		mappingErr         error
		expectedVersion    string
		expectedClientType string
		expectErr          bool
	}{
		{
			name:               "parses standard client version format",
			raw:                "Geth/v1.14.11-stable/linux-amd64/go1.22.1",
			expectedVersion:    "v1.14.11-stable",
			expectedClientType: "geth",
		},
		{
			name:               "parses type version format",
			raw:                "reth/1.2.0-beta.1",
			expectedVersion:    "1.2.0-beta.1",
			expectedClientType: "reth",
		},
		{
			name:               "returns unknown version when slash has no version",
			raw:                "erigon/",
			expectedVersion:    labels.UnknownClientVersion,
			expectedClientType: "erigon",
		},
		{
			name:               "treats bare semver as default client",
			raw:                "v1.13.15",
			expectedVersion:    "v1.13.15",
			expectedClientType: labels.DefaultClient,
		},
		{
			name:               "treats single word as client type",
			raw:                "Nethermind",
			expectedVersion:    labels.UnknownClientVersion,
			expectedClientType: "nethermind",
		},
		{
			name:               "falls back to raw dotted version",
			raw:                "client.build.2024",
			expectedVersion:    "client.build.2024",
			expectedClientType: labels.DefaultClient,
		},
		{
			name:               "handles empty client string",
			raw:                "",
			expectedVersion:    labels.UnknownClientVersion,
			expectedClientType: labels.DefaultClient,
		},
		{
			name:       "returns mapping error",
			mappingErr: errors.New("mapping failed"),
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := labels.NewEthClientLabelsDetector("upstream-id", chains.ETHEREUM, func([]byte) (string, error) {
				return tt.raw, tt.mappingErr
			})

			version, clientType, err := detector.ClientVersionAndType([]byte(`ignored`))

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
