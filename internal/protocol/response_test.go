package protocol_test

import (
	"errors"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/stretchr/testify/assert"
	"io"
	"testing"
)

func TestParseWsSubMessage(t *testing.T) {
	body := []byte(`{"id":"1","jsonrpc":"2.0","result":"0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"}`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, "1", wsResponse.Id)
	assert.Equal(t, protocol.JsonRpc, wsResponse.Type)
	assert.Empty(t, wsResponse.SubId)
	assert.Equal(t, `"0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"`, string(wsResponse.Message))
}

func TestParseWsNumberSubMessage(t *testing.T) {
	body := []byte(`{"id":"12","jsonrpc":"2.0","result": 233242423}`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, "12", wsResponse.Id)
	assert.Equal(t, protocol.JsonRpc, wsResponse.Type)
	assert.Empty(t, wsResponse.SubId)
	assert.Equal(t, `233242423`, string(wsResponse.Message))
}

func TestParseWsEvent(t *testing.T) {
	body := []byte(`{"id":"15","jsonrpc":"2.0","params": { "result": {"key":"value"}, "subscription": "0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"} }`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, "15", wsResponse.Id)
	assert.Equal(t, protocol.Ws, wsResponse.Type)
	assert.Equal(t, "0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f", wsResponse.SubId)
	assert.Equal(t, []byte(`{"key":"value"}`), wsResponse.Message)
}

func TestParseWsEventWithNumSub(t *testing.T) {
	body := []byte(`{"id":"15","jsonrpc":"2.0","params": { "result": {"key":"value"}, "subscription": 1223} }`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, "15", wsResponse.Id)
	assert.Equal(t, protocol.Ws, wsResponse.Type)
	assert.Equal(t, "1223", wsResponse.SubId)
	assert.Equal(t, []byte(`{"key":"value"}`), wsResponse.Message)
}

func TestEncodeJsonRpcRequest(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		id       []byte
		expected []byte
	}{
		{
			name:     "string result",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","result":"0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"}`),
			id:       []byte(`25`),
			expected: []byte(`{"id":25,"jsonrpc":"2.0","result":"0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"}`),
		},
		{
			name:     "bool result",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","result":true}`),
			id:       []byte(`"test"`),
			expected: []byte(`{"id":"test","jsonrpc":"2.0","result":true}`),
		},
		{
			name:     "number result",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","result":12234}`),
			id:       []byte(`"23r23"`),
			expected: []byte(`{"id":"23r23","jsonrpc":"2.0","result":12234}`),
		},
		{
			name:     "object result",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","result":{"key":"value"}}`),
			id:       []byte(`"23r23"`),
			expected: []byte(`{"id":"23r23","jsonrpc":"2.0","result":{"key":"value"}}`),
		},
		{
			name:     "array result",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","result":[{"key":"value"}]}`),
			id:       []byte(`"23r23"`),
			expected: []byte(`{"id":"23r23","jsonrpc":"2.0","result":[{"key":"value"}]}`),
		},
		{
			name:     "error response",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","error":{"message":"error","code":2}}`),
			id:       []byte(`"23r23"`),
			expected: []byte(`{"id":"23r23","jsonrpc":"2.0","error":{"message":"error","code":2}}`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			response := protocol.NewHttpUpstreamResponse("1", test.body, 200, protocol.JsonRpc)

			respReader := response.EncodeResponse(test.id)
			respBytes, err := io.ReadAll(respReader)

			assert.Nil(te, err)
			assert.Equal(te, test.expected, respBytes)
		})
	}
}

func TestEncodeReplyErrorJsonRpc(t *testing.T) {
	replyError := protocol.NewReplyError("1", protocol.ServerErrorWithCause(errors.New("err cause")), protocol.JsonRpc)

	respReader := replyError.EncodeResponse([]byte("55"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.Equal(t, []byte(`{"id":55,"jsonrpc":"2.0","error":{"message":"internal server error: err cause","code":500}}`), respBytes)
}

func TestEncodeReplyErrorRest(t *testing.T) {
	replyError := protocol.NewReplyError("1", protocol.ServerErrorWithCause(errors.New("err cause")), protocol.Rest)

	respReader := replyError.EncodeResponse([]byte("55"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.Equal(t, []byte(`{"message":"internal server error: err cause"}`), respBytes)
}
