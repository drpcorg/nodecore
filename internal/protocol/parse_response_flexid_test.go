package protocol_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
)

// Ethermint/evmos-lineage nodes reply to eth_subscribe with a NUMERIC id even
// when the request carried a string id ({"id":"1"} -> {"id":1}). A strict
// string id used to fail the whole unmarshal, the frame was classified
// Unknown and the ws connection was torn down - leaving such upstreams in a
// permanent reconnect loop with heads only from the resubscribe fallback.
func TestParseJsonRpcWsMessage_NumericIdResultResponse(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","result":"0xad3ddeb5ed159eb69129a94e1b5b8a1c","id":1}`)
	ws := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, ws.Error)
	assert.Equal(t, "1", ws.Id, "numeric id must normalize to its decimal string form")
	assert.Equal(t, protocol.JsonRpc, ws.Type)
	assert.Equal(t, `"0xad3ddeb5ed159eb69129a94e1b5b8a1c"`, string(ws.Message))
}

func TestParseJsonRpcWsMessage_NumericIdErrorResponse(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","error":{"code":-32601,"message":"method not found"},"id":7}`)
	ws := protocol.ParseJsonRpcWsMessage(body)

	assert.NotNil(t, ws.Error)
	assert.Equal(t, "7", ws.Id)
	assert.Equal(t, protocol.JsonRpc, ws.Type)
}

func TestParseJsonRpcWsMessage_NullIdIsIgnored(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","result":"0x1","id":null}`)
	ws := protocol.ParseJsonRpcWsMessage(body)

	assert.Equal(t, "", ws.Id)
	assert.Equal(t, protocol.JsonRpc, ws.Type)
}
