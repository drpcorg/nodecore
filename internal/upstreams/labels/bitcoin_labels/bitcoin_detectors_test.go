package bitcoin_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/bitcoin_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBitcoinClientLabelsDetectorNodeTypeRequest(t *testing.T) {
	detector := bitcoin_labels.NewBitcoinClientLabelsDetector(chains.BITCOIN)

	request, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	require.NotNil(t, request)

	assert.Equal(t, "getnetworkinfo", request.Method())
	assert.Equal(t, protocol.JsonRpc, request.RequestType())

	body, err := request.Body()
	require.NoError(t, err)

	assert.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"getnetworkinfo","params":[]}`, string(body))
}

func TestBitcoinClientLabelsDetectorClientVersionAndType(t *testing.T) {
	tests := []struct {
		name               string
		data               []byte
		expectedVersion    string
		expectedClientType string
		expectErr          bool
	}{
		{
			name:               "parses bitcoin core subversion",
			data:               []byte(`{"version":260100,"subversion":"/Satoshi:26.1.0/","connections":10}`),
			expectedVersion:    "26.1.0",
			expectedClientType: "satoshi",
		},
		{
			name:               "parses dogecoin core subversion",
			data:               []byte(`{"version":1140900,"subversion":"/Shibetoshi:1.14.9/","connections":8}`),
			expectedVersion:    "1.14.9",
			expectedClientType: "shibetoshi",
		},
		{
			name:               "falls back on missing subversion",
			data:               []byte(`{"version":260100,"connections":10}`),
			expectedVersion:    "",
			expectedClientType: "bitcoind",
		},
		{
			name:               "falls back on subversion without version",
			data:               []byte(`{"subversion":"/Satoshi/"}`),
			expectedVersion:    "",
			expectedClientType: "bitcoind",
		},
		{
			name:      "fails on invalid json",
			data:      []byte(`{"subversion":`),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := bitcoin_labels.NewBitcoinClientLabelsDetector(chains.BITCOIN)

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
