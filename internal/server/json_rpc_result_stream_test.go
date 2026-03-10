package server

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamJsonRPCResultExtractsNestedResult(t *testing.T) {
	var out strings.Builder
	input := `{"jsonrpc":"2.0","id":1,"result":{"items":[1,{"k":"v","arr":[true,false,null]}],"s":"x"},"ignored":{"deep":[1,2,3]}}`

	err := streamJsonRPCResult(strings.NewReader(input), &out)
	require.NoError(t, err)
	assert.Equal(t, `{"items":[1,{"k":"v","arr":[true,false,null]}],"s":"x"}`, out.String())
}

func TestStreamJsonRPCResultExtractsPrimitiveResult(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected string
	}{
		{
			name:     "string result",
			response: `{"jsonrpc":"2.0","id":"1","result":"ok"}`,
			expected: `"ok"`,
		},
		{
			name:     "number result",
			response: `{"jsonrpc":"2.0","id":"1","result":12345}`,
			expected: `12345`,
		},
		{
			name:     "boolean result",
			response: `{"jsonrpc":"2.0","id":"1","result":true}`,
			expected: `true`,
		},
		{
			name:     "null result",
			response: `{"jsonrpc":"2.0","id":"1","result":null}`,
			expected: `null`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(te *testing.T) {
			var out strings.Builder
			err := streamJsonRPCResult(strings.NewReader(tc.response), &out)
			require.NoError(te, err)
			assert.Equal(te, tc.expected, out.String())
		})
	}
}

func TestStreamJsonRPCResultMissingResult(t *testing.T) {
	var out strings.Builder
	err := streamJsonRPCResult(strings.NewReader(`{"jsonrpc":"2.0","id":"1","error":{"code":-1}}`), &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "result field is missing")
}

func TestStreamJsonRPCResultInvalidTopLevel(t *testing.T) {
	var out strings.Builder
	err := streamJsonRPCResult(strings.NewReader(`[{"result":1}]`), &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected json object")
}

func TestStreamJsonRPCResultInvalidJSON(t *testing.T) {
	var out strings.Builder
	err := streamJsonRPCResult(strings.NewReader(`{"jsonrpc":"2.0","id":"1","result":{"a":1`), &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse stream response")
}
