package server_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/decoder"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server"
	"github.com/stretchr/testify/assert"
)

type jsonRpcReqWithoutId struct {
	Method string `json:"method"`
	Params any    `json:"params"`
}

func TestCreateJsonRpcHandlerOk(t *testing.T) {
	req := server.Request{Chain: "chain"}
	bodyReader := bytes.NewReader([]byte(`{"method": "eth_test"}`))
	handler, err := server.NewJsonRpcHandler(&req, bodyReader, false)

	assert.Nil(t, err)
	assert.True(t, handler.IsSingle())
	assert.Equal(t, protocol.JsonRpc, handler.GetRequestType())
}

func TestCreateJsonRpcHandlerWithArrayOk(t *testing.T) {
	req := server.Request{Chain: "chain"}
	bodyReader := bytes.NewReader([]byte(`[{"method": "eth_test"}]`))
	handler, err := server.NewJsonRpcHandler(&req, bodyReader, false)

	assert.Nil(t, err)
	assert.False(t, handler.IsSingle())
	assert.Equal(t, protocol.JsonRpc, handler.GetRequestType())
}

func TestCreateJsonRpcHandlerWithEmptyBodyThenError(t *testing.T) {
	req := server.Request{Chain: "chain"}
	bodyReader := bytes.NewReader([]byte(``))
	_, err := server.NewJsonRpcHandler(&req, bodyReader, false)

	assert.True(t, errors.Is(err, decoder.SyntaxError{}))
}

func TestDecodeSingleRequestJsonRpcHandler(t *testing.T) {
	preReq := server.Request{Chain: "chain"}
	bodyReader := bytes.NewReader([]byte(`{"id":1,"method": "eth_test", "params": [false, 0, {"key": "value"}]}`))
	handler, err := server.NewJsonRpcHandler(&preReq, bodyReader, false)

	assert.Nil(t, err)

	req, err := handler.RequestDecode(context.Background())

	assert.Nil(t, err)
	assert.Equal(t, 1, len(req.UpstreamRequests))

	upReq := req.UpstreamRequests[0]
	expected := jsonRpcReqWithoutId{
		Method: "eth_test",
		Params: []interface{}{
			false,
			float64(0),
			map[string]interface{}{"key": "value"},
		},
	}
	body, err := upReq.Body()
	assert.Nil(t, err)

	reqBody := parseBody(body)

	assert.Equal(t, "eth_test", upReq.Method())
	assert.Equal(t, protocol.JsonRpc, upReq.RequestType())
	assert.Equal(t, expected, reqBody)
}

func TestDecodeSingleMultipleJsonRpcHandler(t *testing.T) {
	preReq := server.Request{Chain: "chain"}
	bodyReader := bytes.NewReader([]byte(`[{"id":1,"method": "eth_test", "params": [false, 0, {"key": "value"}]}, {"id":1,"method": "eth_test2"}]`))
	handler, err := server.NewJsonRpcHandler(&preReq, bodyReader, false)

	assert.Nil(t, err)

	req, err := handler.RequestDecode(context.Background())

	assert.Nil(t, err)
	assert.Equal(t, 2, len(req.UpstreamRequests))

	expected := jsonRpcReqWithoutId{
		Method: "eth_test",
		Params: []interface{}{
			false,
			float64(0),
			map[string]interface{}{"key": "value"},
		},
	}
	expected1 := jsonRpcReqWithoutId{
		Method: "eth_test2",
	}
	tests := []struct {
		name     string
		request  protocol.RequestHolder
		expected jsonRpcReqWithoutId
	}{
		{
			request: req.UpstreamRequests[0],
			name:    "first req",

			expected: expected,
		},
		{
			request:  req.UpstreamRequests[1],
			name:     "second req",
			expected: expected1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			body, reqErr := test.request.Body()

			assert.Nil(te, reqErr)
			assert.Equal(t, protocol.JsonRpc, test.request.RequestType())
			assert.Equal(t, test.expected, parseBody(body))
		})
	}
}

func TestEncodeResponseJsonRpcHandlerWithNoRequests(t *testing.T) {
	req := server.Request{Chain: "chain"}
	bodyReader := bytes.NewReader([]byte(`{"method": "eth_test"}`))
	handler, err := server.NewJsonRpcHandler(&req, bodyReader, false)

	assert.Nil(t, err)

	response := testResponseHolder{"23"}
	resp := handler.ResponseEncode(response)

	assert.Equal(t, -1, resp.Order)
}

func TestEncodeResponseJsonRpcHandlerOk(t *testing.T) {
	preReq := server.Request{Chain: "chain"}
	bodyReader := bytes.NewReader([]byte(`{"id":1,"method": "eth_test", "params": [false, 0, {"key": "value"}]}`))
	handler, err := server.NewJsonRpcHandler(&preReq, bodyReader, false)

	assert.Nil(t, err)

	req, err := handler.RequestDecode(context.Background())

	assert.Nil(t, err)
	assert.Equal(t, 1, len(req.UpstreamRequests))

	response := testResponseHolder{id: req.UpstreamRequests[0].Id()}
	resp := handler.ResponseEncode(response)

	assert.Equal(t, 0, resp.Order)
}

func parseBody(body []byte) jsonRpcReqWithoutId {
	var reqBody jsonRpcReqWithoutId
	err := sonic.Unmarshal(body, &reqBody)
	if err != nil {
		panic(err)
	}
	return reqBody
}

type testResponseHolder struct {
	id string
}

func (t testResponseHolder) ResponseCode() int {
	return 0
}

func (t testResponseHolder) ResponseResultString() (string, error) {
	return "", nil
}

func (t testResponseHolder) ResponseResult() []byte {
	return nil
}

func (t testResponseHolder) GetError() *protocol.ResponseError {
	return nil
}

func (t testResponseHolder) EncodeResponse(realId []byte) io.Reader {
	return bytes.NewReader(realId)
}

func (t testResponseHolder) HasError() bool {
	return false
}

func (t testResponseHolder) Id() string {
	return t.id
}

func (t testResponseHolder) HasStream() bool {
	return false
}

var _ protocol.ResponseHolder = (*testResponseHolder)(nil)
