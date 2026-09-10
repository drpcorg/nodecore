package flow

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveLowerBoundFromPrunedErrorUpdatesProofBound(t *testing.T) {
	request := requestWithBlockNumberParam(t, "eth_getProof", []any{"0x343", []string{}, "0xCB5A0A8"}, ".[2]")
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("missing trie node d5648cc9aef48154159d53800f2f"))

	bound, ok := liveLowerBoundFromPrunedError(request.Method(), request.ParseParams(context.Background()), response, 300_000_000)

	require.True(t, ok)
	assert.Equal(t, protocol.ProofBound, bound.Type)
	assert.Equal(t, int64(213229737), bound.Bound)
}

func TestLiveLowerBoundFromPrunedErrorUpdatesTraceBound(t *testing.T) {
	request := requestWithBlockNumberParam(t, "trace_block", []any{"0xCB5A0A8"}, ".[0]")
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("block #1 not found"))

	bound, ok := liveLowerBoundFromPrunedError(request.Method(), request.ParseParams(context.Background()), response, 300_000_000)

	require.True(t, ok)
	assert.Equal(t, protocol.TraceBound, bound.Type)
	assert.Equal(t, int64(213229737), bound.Bound)
}

func TestLiveLowerBoundFromPrunedErrorUpdatesLogsBoundFromRange(t *testing.T) {
	request := requestWithBlockRangeParam(t, "eth_getLogs", []any{map[string]any{"fromBlock": "0xCB5A0A8", "toBlock": "latest"}}, ".[0] | {blockRange: {from: .fromBlock, to: .toBlock}}")
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("history has been pruned"))

	bound, ok := liveLowerBoundFromPrunedError(request.Method(), request.ParseParams(context.Background()), response, 300_000_000)

	require.True(t, ok)
	assert.Equal(t, protocol.LogsBound, bound.Type)
	assert.Equal(t, int64(213229737), bound.Bound)
}

// State methods: the block tag is a blockRef in the eth spec (eth_call, eth_getProof) or a
// blockNumber (eth_getBalance, eth_getCode, eth_getStorageAt); both parse to BlockNumberParam.
func TestLiveLowerBoundFromPrunedErrorUpdatesStateBoundForEthCall(t *testing.T) {
	request := requestWithBlockRefParam(t, "eth_call", []any{map[string]any{"to": "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e", "data": "0x70a08231"}, "0x594caf9"}, ".[1]")
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("missing trie node 67c0d1333ed3edae64944dfa342e11e7364ef8c2d1feb7db13e9f7eacbc49288 (path ) state 0x67c0d1333ed3edae64944dfa342e11e7364ef8c2d1feb7db13e9f7eacbc49288 is not available"))
	bound, ok := liveLowerBoundFromPrunedError(request.Method(), request.ParseParams(context.Background()), response, 93637984)
	require.True(t, ok)
	assert.Equal(t, protocol.StateBound, bound.Type)
	assert.Equal(t, int64(0x594caf9+1), bound.Bound)
}

func TestLiveLowerBoundFromPrunedErrorUpdatesStateBoundForBalanceAndStorage(t *testing.T) {
	for _, tc := range []struct {
		method string
		params []any
		path   string
	}{
		{"eth_getBalance", []any{"0x0000000000000000000000000000000000000000", "0xCB5A0A8"}, ".[1]"},
		{"eth_getCode", []any{"0x0000000000000000000000000000000000000000", "0xCB5A0A8"}, ".[1]"},
		{"eth_getStorageAt", []any{"0x0000000000000000000000000000000000000000", "0x0", "0xCB5A0A8"}, ".[2]"},
	} {
		request := requestWithBlockNumberParam(t, tc.method, tc.params, tc.path)
		response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("missing trie node d5648cc9aef48154159d53800f2f (path ) state 0xd5 is not available"))
		bound, ok := liveLowerBoundFromPrunedError(request.Method(), request.ParseParams(context.Background()), response, 300_000_000)
		require.True(t, ok, tc.method)
		assert.Equal(t, protocol.StateBound, bound.Type, tc.method)
		assert.Equal(t, int64(213229737), bound.Bound, tc.method)
	}
}

func TestLiveLowerBoundFromPrunedErrorSkipsEthCallWithLatestTag(t *testing.T) {
	request := requestWithBlockRefParam(t, "eth_call", []any{map[string]any{"to": "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"}, "latest"}, ".[1]")
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("missing trie node d5648cc9aef48154159d53800f2f"))
	_, ok := liveLowerBoundFromPrunedError(request.Method(), request.ParseParams(context.Background()), response, 300_000_000)
	assert.False(t, ok)
}

// A revert reason is contract-controlled text; even if it embeds a pruned marker it is not a pruned error.
func TestLiveLowerBoundFromPrunedErrorSkipsRevertsEmbeddingMarkers(t *testing.T) {
	request := requestWithBlockRefParam(t, "eth_call", []any{map[string]any{"to": "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"}, "0xCB5A0A8"}, ".[1]")
	for _, msg := range []string{
		"execution reverted: state is not available",
		"execution reverted: block #1 pruned",
		"Execution Reverted: missing trie node abc",
	} {
		response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage(msg))
		_, ok := liveLowerBoundFromPrunedError(request.Method(), request.ParseParams(context.Background()), response, 300_000_000)
		assert.False(t, ok, msg)
	}
}

func TestLiveLowerBoundFromPrunedErrorSkipsNonPrunedErrorsBeforeParsingParams(t *testing.T) {
	request := requestWithBlockNumberParam(t, "eth_getProof", []any{"0x343", []string{}, "not-a-block"}, ".[2]")
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("execution reverted: Fallback not supported"))

	_, ok := liveLowerBoundFromPrunedError(request.Method(), request.ParseParams(context.Background()), response, 300_000_000)

	assert.False(t, ok)
}

func TestLiveLowerBoundFromPrunedErrorSkipsUnsupportedMethod(t *testing.T) {
	request := requestWithBlockNumberParam(t, "eth_getBlockByNumber", []any{"0xCB5A0A8", false}, ".[0]")
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("missing trie node d5648cc9aef48154159d53800f2f"))

	_, ok := liveLowerBoundFromPrunedError(request.Method(), request.ParseParams(context.Background()), response, 300_000_000)

	assert.False(t, ok)
}

func TestLiveLowerBoundFromPrunedErrorSkipsFutureBlock(t *testing.T) {
	request := requestWithBlockNumberParam(t, "eth_getProof", []any{"0x343", []string{}, "0xCB5A0A8"}, ".[2]")
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("missing trie node d5648cc9aef48154159d53800f2f"))

	_, ok := liveLowerBoundFromPrunedError(request.Method(), request.ParseParams(context.Background()), response, 100_000_000)

	assert.False(t, ok)
}

func TestLiveLowerBoundFromPrunedErrorSkipsUnknownHead(t *testing.T) {
	request := requestWithBlockNumberParam(t, "eth_getProof", []any{"0x343", []string{}, "0xCB5A0A8"}, ".[2]")
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("missing trie node d5648cc9aef48154159d53800f2f"))

	_, ok := liveLowerBoundFromPrunedError(request.Method(), request.ParseParams(context.Background()), response, 0)

	assert.False(t, ok)
}

func requestWithBlockNumberParam(t *testing.T, methodName string, params []any, path string) protocol.RequestHolder {
	t.Helper()
	method := specs.MethodWithSettings(methodName, []specs.ApiConnectorType{specs.JsonRpcConnector}, nil, &specs.TagParser{ReturnType: specs.BlockNumberType, Path: path})
	request, err := protocol.NewUpstreamJsonRpcRequestWithSpecMethod(methodName, params, method)
	require.NoError(t, err)
	return request
}

func requestWithBlockRangeParam(t *testing.T, methodName string, params []any, path string) protocol.RequestHolder {
	t.Helper()
	method := specs.MethodWithSettings(methodName, []specs.ApiConnectorType{specs.JsonRpcConnector}, nil, &specs.TagParser{ReturnType: specs.ObjectType, Path: path})
	request, err := protocol.NewUpstreamJsonRpcRequestWithSpecMethod(methodName, params, method)
	require.NoError(t, err)
	return request
}

func requestWithBlockRefParam(t *testing.T, methodName string, params []any, path string) protocol.RequestHolder {
	t.Helper()
	method := specs.MethodWithSettings(methodName, []specs.ApiConnectorType{specs.JsonRpcConnector}, nil, &specs.TagParser{ReturnType: specs.BlockRefType, Path: path})
	request, err := protocol.NewUpstreamJsonRpcRequestWithSpecMethod(methodName, params, method)
	require.NoError(t, err)
	return request
}
