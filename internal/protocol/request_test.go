package protocol_test

import (
	"fmt"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/blake2b"
	"testing"
)

func TestGenerateRequestHashWithoutParams(t *testing.T) {
	request, err := protocol.NewSimpleJsonRpcUpstreamRequest("1", []byte(`1`), "eth_call", nil)

	assert.Nil(t, err)
	expected := fmt.Sprintf("%x", blake2b.Sum256([]byte(request.Method())))
	assert.Equal(t, expected, request.RequestHash())

	request, err = protocol.NewStreamJsonRpcUpstreamRequest("1", []byte(`1`), "eth_call", nil)

	assert.Nil(t, err)
	assert.Equal(t, expected, request.RequestHash())
}

func TestGenerateRequestHashWithParams(t *testing.T) {
	request, err := protocol.NewSimpleJsonRpcUpstreamRequest("1", []byte(`1`), "eth_call", []byte(`"params"`))

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
