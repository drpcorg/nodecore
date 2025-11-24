package connectors_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

			cfg := &config.ApiConnectorConfig{
				Url: "http://localhost:8080",
			}
			connector := connectors.NewHttpConnectorWithDefaultClient(cfg, protocol.JsonRpcConnector, "")
			req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil)

			r := connector.SendRequest(context.Background(), req)

			assert.False(te, r.HasError())
			assert.False(t, r.HasStream())
			require.JSONEq(t, string(test_utils.GetResultAsBytes(test.body)), string(r.ResponseResult()))
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
			body:    []byte(`{"id": "1", "jsonrpc": "2.0", "error": "plain error" }`),
			message: "plain error",
		},
		{
			name:    "with base error",
			body:    []byte(`{"id": "1", "jsonrpc": "2.0", "error": {"code": -2323, "message": "Base error"} }`),
			code:    -2323,
			message: "Base error",
		},
		{
			name:    "with string data error",
			body:    []byte(`{"id": "1", "jsonrpc": "2.0", "error": {"code": -11, "message": "Data error", "data": "data-error"} }`),
			code:    -11,
			message: "Data error",
			data:    "data-error",
		},
		{
			name:    "with object data error",
			body:    []byte(`{"id": "1", "jsonrpc": "2.0", "error": {"code": -111, "message": "Data object error", "data": {"key": "value"}} }`),
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

			cfg := &config.ApiConnectorConfig{
				Url: "http://localhost:8080",
			}
			connector := connectors.NewHttpConnectorWithDefaultClient(cfg, protocol.JsonRpcConnector, "")
			req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil)

			r := connector.SendRequest(context.Background(), req)

			assert.True(te, r.HasError())
			assert.Equal(te, test.code, r.GetError().Code)
			assert.False(t, r.HasStream())
			assert.Equal(te, test.message, r.GetError().Message)
			assert.Equal(te, test.data, r.GetError().Data)
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

	cfg := &config.ApiConnectorConfig{
		Url: "http://localhost:8080",
	}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, protocol.JsonRpcConnector, "")
	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil)

	r := connector.SendRequest(context.Background(), req)

	assert.True(t, r.HasError())
	assert.False(t, r.HasStream())
	assert.Equal(t, -32001, r.GetError().Code)
	assert.Equal(t, "incorrect response body: wrong json-rpc response - there is neither result nor error", r.GetError().Message)
	assert.Nil(t, r.GetError().Data)
	assert.Equal(t, "-32001: incorrect response body: wrong json-rpc response - there is neither result nor error", r.GetError().Error())
}

func TestHttpConnectorType(t *testing.T) {
	tests := []struct {
		name     string
		connType protocol.ApiConnectorType
	}{
		{
			name:     "json-rpc connector",
			connType: protocol.JsonRpcConnector,
		},
		{
			name:     "rest connector",
			connType: protocol.RestConnector,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			cfg := &config.ApiConnectorConfig{
				Url: "http://localhost:8080",
			}
			connector, err := connectors.NewHttpConnector(cfg, test.connType, "")
			assert.NoError(te, err)

			assert.Equal(te, test.connType, connector.GetType())
		})
	}
}

func TestJsonRpcRequest200CodeThenStream(t *testing.T) {
	httpmock.Activate()
	defer httpmock.Deactivate()
	httpmock.RegisterResponder("POST", "", func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(200, []byte(`{"id": 1, "jsonrpc": "2.0", "result": {"number": "0x11"} }`))
		return resp, nil
	})

	cfg := &config.ApiConnectorConfig{
		Url: "http://localhost:8080",
	}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, protocol.JsonRpcConnector, "")
	req := protocol.NewStreamUpstreamJsonRpcRequest("id", json.RawMessage(`"real"`), "eth_test", nil, nil)

	r := connector.SendRequest(context.Background(), req)

	assert.True(t, r.HasStream())
	assert.False(t, r.HasError())
	assert.Nil(t, r.ResponseResult())
}

func TestJsonRpcRequestWithNot200CodeThenNoStream(t *testing.T) {
	httpmock.Activate()
	defer httpmock.Deactivate()

	httpmock.RegisterResponder("POST", "", func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(500, []byte(`{"id": 1, "jsonrpc": "2.0", "error": {"message": "0x11"} }`))
		return resp, nil
	})

	cfg := &config.ApiConnectorConfig{
		Url: "http://localhost:8080",
	}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, protocol.JsonRpcConnector, "")
	req := protocol.NewStreamUpstreamJsonRpcRequest("id", json.RawMessage(`"real"`), "eth_test", nil, nil)

	r := connector.SendRequest(context.Background(), req)

	assert.False(t, r.HasStream())
	assert.True(t, r.HasError())
	assert.Equal(t, &protocol.ResponseError{Message: "0x11", Code: -32000}, r.GetError())
}
