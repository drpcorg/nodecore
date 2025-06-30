package protocol_test

import (
	"errors"
	"github.com/drpcorg/dsheltie/internal/protocol"
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
		hasError bool
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
			hasError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			response := protocol.NewHttpUpstreamResponse("1", test.body, 200, protocol.JsonRpc)

			respReader := response.EncodeResponse(test.id)
			respBytes, err := io.ReadAll(respReader)

			assert.Nil(te, err)
			assert.False(te, response.HasStream())
			assert.Equal(te, "1", response.Id())
			assert.Equal(te, test.hasError, response.HasError())
			assert.Equal(te, test.expected, respBytes)
		})
	}
}

func TestEncodeReplyErrorJsonRpc(t *testing.T) {
	replyError := protocol.NewReplyError("1", protocol.ServerErrorWithCause(errors.New("err cause")), protocol.JsonRpc, protocol.TotalFailure)

	respReader := replyError.EncodeResponse([]byte("55"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.True(t, replyError.HasError())
	assert.Equal(t, "1", replyError.Id())
	assert.False(t, replyError.HasStream())
	assert.Nil(t, replyError.ResponseResult())
	assert.Equal(t, protocol.ServerErrorWithCause(errors.New("err cause")), replyError.GetError())
	assert.Equal(t, []byte(`{"id":55,"jsonrpc":"2.0","error":{"message":"internal server error: err cause","code":500}}`), respBytes)
}

func TestEncodeReplyErrorRest(t *testing.T) {
	replyError := protocol.NewReplyError("1", protocol.ServerErrorWithCause(errors.New("err cause")), protocol.Rest, protocol.TotalFailure)

	respReader := replyError.EncodeResponse([]byte("55"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.Equal(t, "1", replyError.Id())
	assert.True(t, replyError.HasError())
	assert.False(t, replyError.HasStream())
	assert.Nil(t, replyError.ResponseResult())
	assert.Equal(t, protocol.ServerErrorWithCause(errors.New("err cause")), replyError.GetError())
	assert.Equal(t, []byte(`{"message":"internal server error: err cause"}`), respBytes)
}

func TestEncodeWsJsonRpcResponse(t *testing.T) {
	response := protocol.NewWsJsonRpcResponse("2", []byte("result"), nil)

	respReader := response.EncodeResponse([]byte("32"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.False(t, response.HasError())
	assert.Nil(t, response.GetError())
	assert.Equal(t, []byte("result"), response.ResponseResult())
	assert.Equal(t, []byte(`{"id":32,"jsonrpc":"2.0","result":result}`), respBytes)
}

func TestEncodeWsJsonRpcResponseWithError(t *testing.T) {
	response := protocol.NewWsJsonRpcResponse("2", []byte("error"), protocol.ServerError())

	respReader := response.EncodeResponse([]byte("32"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.True(t, response.HasError())
	assert.False(t, response.HasStream())
	assert.Equal(t, "2", response.Id())
	assert.Equal(t, []byte("error"), response.ResponseResult())
	assert.Equal(t, protocol.ServerError(), response.GetError())
	assert.Equal(t, []byte(`{"id":32,"jsonrpc":"2.0","error":error}`), respBytes)
}

func TestEncodeSubscriptionEventResponse(t *testing.T) {
	result := []byte("event")
	response := protocol.NewSubscriptionEventResponse("11", result)

	respReader := response.EncodeResponse([]byte("32"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.False(t, response.HasError())
	assert.Equal(t, "11", response.Id())
	assert.Nil(t, response.GetError())
	assert.False(t, response.HasStream())
	assert.Equal(t, result, response.ResponseResult())
	assert.Equal(t, result, respBytes)
}
