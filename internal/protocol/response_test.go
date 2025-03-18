package protocol_test

import (
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestParseWsSubMessage(t *testing.T) {
	body := []byte(`{"id":1,"jsonrpc":"2.0","result":"0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"}`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, uint64(1), wsResponse.Id)
	assert.Equal(t, protocol.JsonRpc, wsResponse.Type)
	assert.Empty(t, wsResponse.SubId)
	assert.Equal(t, `"0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"`, string(wsResponse.Message))
}

func TestParseWsNumberSubMessage(t *testing.T) {
	body := []byte(`{"id":12,"jsonrpc":"2.0","result": 233242423}`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, uint64(12), wsResponse.Id)
	assert.Equal(t, protocol.JsonRpc, wsResponse.Type)
	assert.Empty(t, wsResponse.SubId)
	assert.Equal(t, `233242423`, string(wsResponse.Message))
}

func TestParseWsEvent(t *testing.T) {
	body := []byte(`{"id":15,"jsonrpc":"2.0","params": { "result": {"key": "value"}, "subscription": "0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"} }`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, uint64(15), wsResponse.Id)
	assert.Equal(t, protocol.Ws, wsResponse.Type)
	assert.Equal(t, "0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f", wsResponse.SubId)
	assert.Equal(t, []byte(`{"key":"value"}`), wsResponse.Message)
}

func TestParseWsEventWithNumSub(t *testing.T) {
	body := []byte(`{"id":15,"jsonrpc":"2.0","params": { "result": {"key": "value"}, "subscription": 1223} }`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, uint64(15), wsResponse.Id)
	assert.Equal(t, protocol.Ws, wsResponse.Type)
	assert.Equal(t, "1223", wsResponse.SubId)
	assert.Equal(t, []byte(`{"key":"value"}`), wsResponse.Message)
}
