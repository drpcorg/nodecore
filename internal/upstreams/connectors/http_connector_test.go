package connectors_test

import (
	"context"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/internal/upstreams/connectors"
	"github.com/drpcorg/dshaltie/pkg/utils"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestReceiveJsonRpcResponseWithResult(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "with result object",
			body: []byte(`{"id": 1, "jsonrpc": "2.0", "result": {"number": "0x11"} }`),
		},
		{
			name: "with result bool",
			body: []byte(`{"id": 1, "jsonrpc": "2.0", "result": false }`),
		},
		{
			name: "with result number",
			body: []byte(`{"id": 1, "jsonrpc": "2.0", "result": 1122 }`),
		},
		{
			name: "with result string",
			body: []byte(`{"id": 1, "jsonrpc": "2.0", "result": "test" }`),
		},
		{
			name: "with result null",
			body: []byte(`{"id": 1, "jsonrpc": "2.0", "result": null }`),
		},
		{
			name: "with result array",
			body: []byte(`{"id": 1, "jsonrpc": "2.0", "result": [{"num": 1}, {"num": 2}, {"num": 3}] }`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			httpmock.Activate()
			defer httpmock.Deactivate()

			httpmock.RegisterResponder("POST", "", func(request *http.Request) (*http.Response, error) {
				resp := httpmock.NewBytesResponse(200, test.body)
				return resp, nil
			})

			connector := connectors.NewHttpConnector("http://localhost:8080", protocol.JsonRpcConnector, nil)
			req, _ := protocol.NewJsonRpcUpstreamRequest(1, "eth_test", nil, false)

			r := connector.SendRequest(context.Background(), req)

			assert.False(te, r.HasError())
			require.JSONEq(t, string(utils.GetResultAsBytes(test.body)), string(r.ResponseResult()))
		})
	}
}

func TestReceiveJsonRpcResponseWithError(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		code    int
		message string
		data    interface{}
	}{
		{
			name:    "with plain string error",
			body:    []byte(`{"id": 1, "jsonrpc": "2.0", "error": "plain error" }`),
			message: "plain error",
		},
		{
			name:    "with base error",
			body:    []byte(`{"id": 1, "jsonrpc": "2.0", "error": {"code": -2323, "message": "Base error"} }`),
			code:    -2323,
			message: "Base error",
		},
		{
			name:    "with string data error",
			body:    []byte(`{"id": 1, "jsonrpc": "2.0", "error": {"code": -11, "message": "Data error", "data": "data-error"} }`),
			code:    -11,
			message: "Data error",
			data:    "data-error",
		},
		{
			name:    "with object data error",
			body:    []byte(`{"id": 1, "jsonrpc": "2.0", "error": {"code": -111, "message": "Data object error", "data": {"key": "value"}} }`),
			code:    -111,
			message: "Data object error",
			data: map[string]interface{}{
				"key": "value",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			httpmock.Activate()
			defer httpmock.Deactivate()

			httpmock.RegisterResponder("POST", "", func(request *http.Request) (*http.Response, error) {
				resp := httpmock.NewBytesResponse(200, test.body)
				return resp, nil
			})

			connector := connectors.NewHttpConnector("http://localhost:8080", protocol.JsonRpcConnector, nil)
			req, _ := protocol.NewJsonRpcUpstreamRequest(1, "eth_test", nil, false)

			r := connector.SendRequest(context.Background(), req)

			assert.True(te, r.HasError())
			assert.Equal(te, test.code, r.ResponseError().Code)
			assert.Equal(te, test.message, r.ResponseError().Message)
			assert.Equal(te, test.data, r.ResponseError().Data)
		})
	}
}

func TestIncorrectJsonRpcResponseBodyThenError(t *testing.T) {
	httpmock.Activate()
	defer httpmock.Deactivate()

	httpmock.RegisterResponder("POST", "", func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(200, []byte("a[sdasdas]w2w"))
		return resp, nil
	})

	connector := connectors.NewHttpConnector("http://localhost:8080", protocol.JsonRpcConnector, nil)
	req, _ := protocol.NewJsonRpcUpstreamRequest(1, "eth_test", nil, false)

	r := connector.SendRequest(context.Background(), req)

	assert.True(t, r.HasError())
	assert.Equal(t, 1, r.ResponseError().Code)
	assert.Equal(t, "incorrect response body", r.ResponseError().Message)
	assert.Nil(t, r.ResponseError().Data)
	assert.Equal(t, "1: incorrect response body, caused by: wrong json-rpc response - there is neither result nor error", r.ResponseError().Error())
}
