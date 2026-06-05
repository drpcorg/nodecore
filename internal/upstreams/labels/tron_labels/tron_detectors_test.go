package tron_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/tron_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTronClientLabelsDetectorNodeTypeRequest(t *testing.T) {
	detector := tron_labels.NewTronClientLabelsDetector(chains.TRON)

	request, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	require.NotNil(t, request)

	assert.Equal(t, "POST#/wallet/getnodeinfo", request.Method())
	assert.Equal(t, protocol.Rest, request.RequestType())
	assert.False(t, request.IsStream())
	assert.False(t, request.IsSubscribe())
	assert.NotNil(t, request.RequestObserver())
	assert.Equal(t, protocol.InternalUnary, request.RequestObserver().GetRequestKind())

	body, err := request.Body()
	require.NoError(t, err)
	assert.Nil(t, body)
}

func TestTronClientLabelsDetectorClientVersionAndType(t *testing.T) {
	tests := []struct {
		name            string
		data            string
		expectedVersion string
		expectedType    string
		expectErr       bool
	}{
		{
			name:            "extracts codeVersion",
			data:            `{"configNodeInfo":{"codeVersion":"4.7.5"}}`,
			expectedVersion: "4.7.5",
			expectedType:    "tron",
		},
		{
			name:            "tolerates extra fields",
			data:            `{"configNodeInfo":{"codeVersion":"4.8.0","p2pVersion":"11111"},"machineInfo":{}}`,
			expectedVersion: "4.8.0",
			expectedType:    "tron",
		},
		{
			name:            "missing codeVersion gives empty version but tron type",
			data:            `{"configNodeInfo":{}}`,
			expectedVersion: "",
			expectedType:    "tron",
		},
		{
			name:            "missing configNodeInfo gives empty version but tron type",
			data:            `{}`,
			expectedVersion: "",
			expectedType:    "tron",
		},
		{
			name:      "invalid json returns error",
			data:      `{"configNodeInfo":`,
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			detector := tron_labels.NewTronClientLabelsDetector(chains.TRON)

			version, clientType, err := detector.ClientVersionAndType([]byte(tc.data))
			if tc.expectErr {
				require.Error(t, err)
				assert.Empty(t, version)
				assert.Empty(t, clientType)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expectedVersion, version)
			assert.Equal(t, tc.expectedType, clientType)
		})
	}
}
