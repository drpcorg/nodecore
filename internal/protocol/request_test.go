package protocol_test

import (
	"context"
	"fmt"
	"github.com/drpcorg/dsheltie/internal/protocol"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/blake2b"
	"testing"
)

func TestGenerateRequestHashWithoutParams(t *testing.T) {
	request := protocol.NewUpstreamJsonRpcRequest("1", []byte(`1`), "eth_call", nil, false, nil)

	expected := fmt.Sprintf("%x", blake2b.Sum256([]byte(request.Method())))
	assert.Equal(t, expected, request.RequestHash())

	request = protocol.NewStreamUpstreamJsonRpcRequest("1", []byte(`1`), "eth_call", nil, nil)

	assert.Equal(t, expected, request.RequestHash())
}

func TestGenerateRequestHashWithParams(t *testing.T) {
	request := protocol.NewUpstreamJsonRpcRequest("1", []byte(`1`), "eth_call", []byte(`"params"`), false, nil)

	expected := fmt.Sprintf("%x", blake2b.Sum256(append([]byte(`"params"`), []byte(request.Method())...)))
	assert.Equal(t, expected, request.RequestHash())

	request = protocol.NewStreamUpstreamJsonRpcRequest("1", []byte(`1`), "eth_call", []byte(`"params"`), nil)

	assert.Equal(t, expected, request.RequestHash())
}

func TestNotRequestHashForInternalJsonRpcRequest(t *testing.T) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_call", []byte(`"params"`))

	assert.Nil(t, err)
	assert.Empty(t, request.RequestHash())
}

func TestHttpRequestParseParamWithoutMethodThenNil(t *testing.T) {
	request := protocol.NewUpstreamJsonRpcRequest("1", []byte(`1`), "eth_call", nil, false, nil)

	param := request.ParseParams(context.Background())
	assert.Nil(t, param)
}

func TestHttpRequestParseParams(t *testing.T) {
	tagParser := specs.TagParser{ReturnType: specs.BlockNumberType, Path: ".[1]"}
	method := specs.MethodWithSettings("eth_call", nil, &tagParser)
	request := protocol.NewUpstreamJsonRpcRequest("1", []byte(`1`), "eth_call", []byte(`[false, "0x4"]`), false, method)

	param := request.ParseParams(context.Background())
	assert.IsType(t, &specs.BlockNumberParam{}, param)
	assert.Equal(t, rpc.BlockNumber(4), param.(*specs.BlockNumberParam).BlockNumber)
}

func TestUpstreamRequestParseAndModifyParams(t *testing.T) {
	tagParser := specs.TagParser{ReturnType: specs.StringType, Path: ".[2].hash"}
	method := specs.MethodWithSettings("eth_call", &specs.MethodSettings{Sticky: &specs.Sticky{SendSticky: true}}, &tagParser)
	request := protocol.NewUpstreamJsonRpcRequest("1", []byte(`1`), "eth_call", []byte(`[false, "0x4", {"hash": "235"}]`), false, method)

	param := request.ParseParams(context.Background())
	assert.IsType(t, &specs.StringParam{}, param)
	assert.Equal(t, "235", param.(*specs.StringParam).Value)

	request.ModifyParams(context.Background(), "superValue")

	param = request.ParseParams(context.Background())
	assert.IsType(t, &specs.StringParam{}, param)
	assert.Equal(t, "superValue", param.(*specs.StringParam).Value)
}
