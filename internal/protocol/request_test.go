package protocol_test

import (
	"context"
	"fmt"
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
	request := protocol.NewUpstreamJsonRpcRequest("1", body, false, nil)

	expected := fmt.Sprintf("%x", blake2b.Sum256([]byte(request.Method())))
	assert.Equal(t, expected, request.RequestHash())

	request = protocol.NewStreamUpstreamJsonRpcRequest("1", body, nil)

	assert.Equal(t, expected, request.RequestHash())
}

func TestGenerateRequestHashWithParams(t *testing.T) {
	body := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call", Params: []byte(`"params"`)}
	request := protocol.NewUpstreamJsonRpcRequest("1", body, false, nil)

	expected := fmt.Sprintf("%x", blake2b.Sum256(append([]byte(`"params"`), []byte(request.Method())...)))
	assert.Equal(t, expected, request.RequestHash())

	body = protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call", Params: []byte(`"params"`)}
	request = protocol.NewStreamUpstreamJsonRpcRequest("1", body, nil)

	assert.Equal(t, expected, request.RequestHash())
}

func TestNotRequestHashForInternalJsonRpcRequest(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_call", []byte(`"params"`), chains.ETHEREUM)

	assert.Nil(t, err)
	assert.Empty(t, request.RequestHash())
}

func TestHttpRequestParseParamWithoutMethodThenNil(t *testing.T) {
	body := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call", Params: nil}
	request := protocol.NewUpstreamJsonRpcRequest("1", body, false, nil)

	param := request.ParseParams(context.Background())
	assert.Nil(t, param)
}

func TestHttpRequestParseParams(t *testing.T) {
	tagParser := specs.TagParser{ReturnType: specs.BlockNumberType, Path: ".[1]"}
	method := specs.MethodWithSettings("eth_call", []specs.ApiConnectorType{specs.JsonRpcConnector}, nil, &tagParser)
	body := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call", Params: []byte(`[false, "0x4"]`)}
	request := protocol.NewUpstreamJsonRpcRequest("1", body, false, method)

	param := request.ParseParams(context.Background())
	assert.IsType(t, &specs.BlockNumberParam{}, param)
	assert.Equal(t, rpc.BlockNumber(4), param.(*specs.BlockNumberParam).BlockNumber)
}

func TestUpstreamRequestParseAndModifyParams(t *testing.T) {
	tagParser := specs.TagParser{ReturnType: specs.StringType, Path: ".[2].hash"}
	method := specs.MethodWithSettings("eth_call", []specs.ApiConnectorType{specs.JsonRpcConnector}, &specs.MethodSettings{Sticky: &specs.Sticky{SendSticky: true}}, &tagParser)
	body := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call", Params: []byte(`[false, "0x4", {"hash": "235"}]`)}
	request := protocol.NewUpstreamJsonRpcRequest("1", body, false, method)

	param := request.ParseParams(context.Background())
	assert.IsType(t, &specs.StringParam{}, param)
	assert.Equal(t, "235", param.(*specs.StringParam).Value)

	request.ModifyParams(context.Background(), "superValue")

	param = request.ParseParams(context.Background())
	assert.IsType(t, &specs.StringParam{}, param)
	assert.Equal(t, "superValue", param.(*specs.StringParam).Value)
}
