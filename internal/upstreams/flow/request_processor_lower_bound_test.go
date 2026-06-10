package flow

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	specs "github.com/drpcorg/nodecore/pkg/methods"
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
