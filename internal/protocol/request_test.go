package protocol_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/blake2b"
)

func TestGenerateRequestHashWithoutParams(t *testing.T) {
	body := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call", Params: nil}
	request := protocol.NewUpstreamJsonRpcRequest("1", body, false, "")

	expected := fmt.Sprintf("%x", blake2b.Sum256([]byte(request.Method())))
	assert.Equal(t, expected, request.RequestHash())

	request = protocol.NewStreamUpstreamJsonRpcRequest("1", body, "")

	assert.Equal(t, expected, request.RequestHash())
}

func TestGenerateRequestHashWithParams(t *testing.T) {
	body := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call", Params: []byte(`"params"`)}
	request := protocol.NewUpstreamJsonRpcRequest("1", body, false, "")

	expected := fmt.Sprintf("%x", blake2b.Sum256(append([]byte(`"params"`), []byte(request.Method())...)))
	assert.Equal(t, expected, request.RequestHash())

	body = protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call", Params: []byte(`"params"`)}
	request = protocol.NewStreamUpstreamJsonRpcRequest("1", body, "")

	assert.Equal(t, expected, request.RequestHash())
}

func TestRequestHashForInternalJsonRpcRequest(t *testing.T) {
	// RequestHash is now computed lazily on first access, so internal requests
	// produce a real, stable hash (previously empty). They still bypass the
	// cache in practice, so this only removes the empty-key aliasing risk.
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_call", []byte(`"params"`), chains.ETHEREUM)
	assert.Nil(t, err)

	hash := request.RequestHash()
	assert.NotEmpty(t, hash)
	assert.Equal(t, hash, request.RequestHash()) // memoized: same value on repeated calls

	other, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_call", []byte(`"params"`), chains.ETHEREUM)
	assert.Nil(t, err)
	assert.Equal(t, hash, other.RequestHash()) // deterministic across instances
}

func TestInternalJsonRpcRequestNilParamsEncodedAsEmptyArray(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	assert.NoError(t, err)

	body, err := request.Body()
	assert.NoError(t, err)
	assert.JSONEq(t, `{"jsonrpc":"2.0","id":"1","method":"eth_chainId","params":[]}`, string(body))
}

func TestHttpRequestParseParamWithoutMethodThenNil(t *testing.T) {
	body := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call", Params: nil}
	request := protocol.NewUpstreamJsonRpcRequest("1", body, false, "")

	param := request.ParseParams(context.Background())
	assert.Nil(t, param)
}

func TestHttpRequestParseParams(t *testing.T) {
	tagParser := specs.TagParser{ReturnType: specs.BlockNumberType, Path: ".[1]"}
	method := specs.MethodWithSettings("eth_call", []specs.ApiConnectorType{specs.JsonRpcConnector}, nil, &tagParser)
	request, err := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("eth_call", []any{false, "0x4"}, method)
	assert.NoError(t, err)

	param := request.ParseParams(context.Background())
	assert.IsType(t, &specs.BlockNumberParam{}, param)
	assert.Equal(t, rpc.BlockNumber(4), param.(*specs.BlockNumberParam).BlockNumber)
}

func TestUpstreamRequestParseAndModifyParams(t *testing.T) {
	tagParser := specs.TagParser{ReturnType: specs.StringType, Path: ".[2].hash"}
	method := specs.MethodWithSettings("eth_call", []specs.ApiConnectorType{specs.JsonRpcConnector}, &specs.MethodSettings{Sticky: &specs.Sticky{SendSticky: true}}, &tagParser)
	request, err := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("eth_call", []any{false, "0x4", map[string]string{"hash": "235"}}, method)
	assert.NoError(t, err)

	param := request.ParseParams(context.Background())
	assert.IsType(t, &specs.StringParam{}, param)
	assert.Equal(t, "235", param.(*specs.StringParam).Value)

	request.ModifyParams(context.Background(), "superValue")

	param = request.ParseParams(context.Background())
	assert.IsType(t, &specs.StringParam{}, param)
	assert.Equal(t, "superValue", param.(*specs.StringParam).Value)
}

func TestRequestHashIsRaceFreeWithModifyParams(t *testing.T) {
	// RequestHash and ModifyParams never run concurrently in production
	// (cacheable vs sticky-send method sets are disjoint), but RequestHash
	// reads u.requestParams while ModifyParams writes it, so the access must be
	// synchronized lest a future caller make them overlap. Drive both
	// concurrently under -race to prove the field access is locked.
	tagParser := specs.TagParser{ReturnType: specs.StringType, Path: ".[2].hash"}
	method := specs.MethodWithSettings("eth_call", []specs.ApiConnectorType{specs.JsonRpcConnector}, &specs.MethodSettings{Sticky: &specs.Sticky{SendSticky: true}}, &tagParser)
	request, err := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("eth_call", []any{false, "0x4", map[string]string{"hash": "235"}}, method)
	assert.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = request.RequestHash() }()
		go func() { defer wg.Done(); request.ModifyParams(context.Background(), "superValue") }()
	}
	wg.Wait()

	assert.NotEmpty(t, request.RequestHash())
}
