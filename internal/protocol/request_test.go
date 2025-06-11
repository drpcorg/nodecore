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
	request, err := protocol.NewSimpleJsonRpcUpstreamRequest("1", []byte(`1`), "eth_call", nil, false)

	assert.Nil(t, err)
	expected := fmt.Sprintf("%x", blake2b.Sum256([]byte(request.Method())))
	assert.Equal(t, expected, request.RequestHash())

	request, err = protocol.NewStreamJsonRpcUpstreamRequest("1", []byte(`1`), "eth_call", nil)

	assert.Nil(t, err)
	assert.Equal(t, expected, request.RequestHash())
}

func TestGenerateRequestHashWithParams(t *testing.T) {
	request, err := protocol.NewSimpleJsonRpcUpstreamRequest("1", []byte(`1`), "eth_call", []byte(`"params"`), false)

	assert.Nil(t, err)
	expected := fmt.Sprintf("%x", blake2b.Sum256(append([]byte(`"params"`), []byte(request.Method())...)))
	assert.Equal(t, expected, request.RequestHash())

	request, err = protocol.NewStreamJsonRpcUpstreamRequest("1", []byte(`1`), "eth_call", []byte(`"params"`))

	assert.Nil(t, err)
	assert.Equal(t, expected, request.RequestHash())
}

func TestNotRequestHashForInternalJsonRpcRequest(t *testing.T) {
	request, err := protocol.NewInternalJsonRpcUpstreamRequest("eth_call", []byte(`"params"`))

	assert.Nil(t, err)
	assert.Empty(t, request.RequestHash())
}

func TestRestRequestHashFromMethod(t *testing.T) {
	request := protocol.NewHttpUpstreamRequest("eth_call", nil, nil)
	expected := fmt.Sprintf("%x", blake2b.Sum256([]byte(request.Method())))

	assert.Equal(t, expected, request.RequestHash())
}

func TestRestRequestHashFromBody(t *testing.T) {
	request := protocol.NewHttpUpstreamRequest("eth_call", nil, []byte(`body`))
	expected := fmt.Sprintf("%x", blake2b.Sum256([]byte(`body`)))

	assert.Equal(t, expected, request.RequestHash())
}

func TestHttpRequestParseParamWithoutMethodThenNil(t *testing.T) {
	request, err := protocol.NewSimpleJsonRpcUpstreamRequest("1", []byte(`1`), "eth_call", nil, false)
	assert.Nil(t, err)

	param := request.ParseParams(context.Background(), nil)
	assert.Nil(t, param)
}

func TestHttpRequestParseParams(t *testing.T) {
	tagParser := specs.TagParser{ReturnType: specs.BlockNumberType, Path: ".[1]"}
	method := specs.MethodWithSettings("eth_call", nil, &tagParser)
	request, err := protocol.NewSimpleJsonRpcUpstreamRequest("1", []byte(`1`), "eth_call", []byte(`[false, "0x4"]`), false)
	assert.Nil(t, err)

	param := request.ParseParams(context.Background(), method)
	assert.IsType(t, &specs.BlockNumberParam{}, param)
	assert.Equal(t, rpc.BlockNumber(4), param.(*specs.BlockNumberParam).BlockNumber)
}
