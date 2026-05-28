package connectors_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/methods"
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
			httpmock.Activate(t)
			defer httpmock.Deactivate()

			httpmock.RegisterResponder("POST", "", func(request *http.Request) (*http.Response, error) {
				resp := httpmock.NewBytesResponse(200, test.body)
				return resp, nil
			})

			cfg := &config.ApiConnectorConfig{
				Url: "http://localhost:8080",
			}
			connector := connectors.NewHttpConnectorWithDefaultClient(cfg, specs.JsonRpcConnector, "")
			req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, chains.ETHEREUM)

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
			httpmock.Activate(te)
			defer httpmock.Deactivate()

			httpmock.RegisterResponder("POST", "", func(request *http.Request) (*http.Response, error) {
				resp := httpmock.NewBytesResponse(200, test.body)
				return resp, nil
			})

			cfg := &config.ApiConnectorConfig{
				Url: "http://localhost:8080",
			}
			connector := connectors.NewHttpConnectorWithDefaultClient(cfg, specs.JsonRpcConnector, "")
			req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, chains.ETHEREUM)

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
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterResponder("POST", "", func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(200, []byte("a[sdasdas]w2w"))
		return resp, nil
	})

	cfg := &config.ApiConnectorConfig{
		Url: "http://localhost:8080",
	}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, specs.JsonRpcConnector, "")
	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, chains.ETHEREUM)

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
		connType specs.ApiConnectorType
	}{
		{
			name:     "json-rpc connector",
			connType: specs.JsonRpcConnector,
		},
		{
			name:     "rest connector",
			connType: specs.RestConnector,
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
	httpmock.Activate(t)
	defer httpmock.Deactivate()
	httpmock.RegisterResponder("POST", "", func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(200, []byte(`{"id": 1, "jsonrpc": "2.0", "result": {"number": "0x11"} }`))
		return resp, nil
	})

	cfg := &config.ApiConnectorConfig{
		Url: "http://localhost:8080",
	}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, specs.JsonRpcConnector, "")
	jsonBody := protocol.JsonRpcRequestBody{Id: json.RawMessage(`"real"`), Method: "eth_test", Params: nil}
	req := protocol.NewStreamUpstreamJsonRpcRequest("id", jsonBody, "")

	r := connector.SendRequest(context.Background(), req)

	assert.True(t, r.HasStream())
	assert.False(t, r.HasError())
	assert.Nil(t, r.ResponseResult())
}

func TestJsonRpcRequestWithNot200CodeThenNoStream(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterResponder("POST", "", func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(500, []byte(`{"id": 1, "jsonrpc": "2.0", "error": {"message": "0x11"} }`))
		return resp, nil
	})

	cfg := &config.ApiConnectorConfig{
		Url: "http://localhost:8080",
	}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, specs.JsonRpcConnector, "")
	jsonBody := protocol.JsonRpcRequestBody{Id: json.RawMessage(`"real"`), Method: "eth_test", Params: nil}
	req := protocol.NewStreamUpstreamJsonRpcRequest("id", jsonBody, "")

	r := connector.SendRequest(context.Background(), req)

	assert.False(t, r.HasStream())
	assert.True(t, r.HasError())
	assert.Equal(t, &protocol.ResponseError{Message: "0x11", Code: -32000}, r.GetError())
}

func TestHttpConnectorSubscribeStates(t *testing.T) {
	cfg := &config.ApiConnectorConfig{
		Url: "http://localhost:8080",
	}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, specs.JsonRpcConnector, "")

	sub := connector.SubscribeStates("name")

	assert.Nil(t, sub)
}

// =======================
// REST connector tests
// =======================
//
// These cover the sendRest path: template expansion via utils.BuildRestURL,
// query/header layering, response-header attachment, and the multi-tier
// REST error parsing (parseRestErrorBody). Each test uses a captured
// *http.Request inside the responder so assertions can inspect the verb,
// path, query, and headers the connector actually built.

func newRestConnector(t *testing.T, cfg *config.ApiConnectorConfig) *connectors.HttpConnector {
	t.Helper()
	return connectors.NewHttpConnectorWithDefaultClient(cfg, specs.RestConnector, "")
}

func TestRestRequest_LiteralTemplateExpandsToVerbAndPath(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	var gotMethod, gotPath string
	httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			gotMethod = req.Method
			gotPath = req.URL.Path
			return httpmock.NewBytesResponse(200, []byte(`{"ok":true}`)), nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
	req := protocol.NewUpstreamRestRequest("1", "POST#/exchange", nil, []byte(`{"x":1}`), "")

	r := connector.SendRequest(context.Background(), req)

	require.False(t, r.HasError(), "got unexpected error: %+v", r.GetError())
	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, "/exchange", gotPath)
	assert.Equal(t, []byte(`{"ok":true}`), r.ResponseResult())
}

func TestRestRequest_WildcardTemplateUsesPathParamsInOrder(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	var gotPath string
	httpmock.RegisterResponder("GET", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			return httpmock.NewBytesResponse(200, []byte(`[]`)), nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
	req := protocol.NewUpstreamRestRequest(
		"1",
		"GET#/v2/blocks/*/tx/*",
		&protocol.RequestParams{PathParams: []string{"42", "abc"}},
		nil,
		"",
	)

	r := connector.SendRequest(context.Background(), req)

	require.False(t, r.HasError())
	assert.Equal(t, "/v2/blocks/42/tx/abc", gotPath,
		"wildcards must be substituted in order from RequestParams.PathParams")
}

func TestRestRequest_QueryParamsForwardedAndMultiValuedPreserved(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	var gotQuery url.Values
	httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			gotQuery = req.URL.Query()
			return httpmock.NewBytesResponse(200, []byte(`{}`)), nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
	req := protocol.NewUpstreamRestRequest(
		"1",
		"POST#/exchange",
		&protocol.RequestParams{
			QueryParams: map[string][]string{
				"token":  {"A", "B"},
				"format": {"json"},
			},
		},
		nil, "",
	)

	r := connector.SendRequest(context.Background(), req)

	require.False(t, r.HasError())
	assert.Equal(t, []string{"A", "B"}, gotQuery["token"],
		"repeated query values must reach the upstream")
	assert.Equal(t, []string{"json"}, gotQuery["format"])
}

// The endpoint config can carry its own query string (e.g. an API key). It
// must survive the path/query join and not get overwritten by the
// per-request query merge.
func TestRestRequest_EndpointConfigQueryIsPreserved(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	var gotQuery url.Values
	var gotPath string
	httpmock.RegisterResponder("GET", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			gotQuery = req.URL.Query()
			gotPath = req.URL.Path
			return httpmock.NewBytesResponse(200, []byte(`{}`)), nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{Url: "http://localhost:8080/v1?apikey=secret"})
	req := protocol.NewUpstreamRestRequest(
		"1",
		"GET#/status",
		&protocol.RequestParams{QueryParams: map[string][]string{"format": {"json"}}},
		nil, "",
	)

	r := connector.SendRequest(context.Background(), req)

	require.False(t, r.HasError())
	assert.Equal(t, "secret", gotQuery.Get("apikey"),
		"connector-config apikey must survive the per-request query merge")
	assert.Equal(t, "json", gotQuery.Get("format"))
	assert.Equal(t, "/v1/status", gotPath)
}

func TestRestRequest_ClientHeadersForwarded(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	var gotHeaders http.Header
	httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			gotHeaders = req.Header.Clone()
			return httpmock.NewBytesResponse(200, []byte(`{}`)), nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
	req := protocol.NewUpstreamRestRequest(
		"1",
		"POST#/exchange",
		&protocol.RequestParams{
			Headers: map[string][]string{
				"X-Custom": {"hello"},
				"X-Multi":  {"one", "two"},
			},
		},
		nil, "",
	)

	r := connector.SendRequest(context.Background(), req)

	require.False(t, r.HasError())
	assert.Equal(t, []string{"hello"}, gotHeaders.Values("X-Custom"))
	assert.Equal(t, []string{"one", "two"}, gotHeaders.Values("X-Multi"),
		"repeated client headers must survive the round-trip")
}

// Connector-config headers (auth tokens, API keys) must win over any
// per-request client header at the same key - a curious client must not
// be able to inject or override them.
func TestRestRequest_ConfigHeadersWinOverClientHeaders(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	var gotHeader string
	httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			gotHeader = req.Header.Get("Authorization")
			return httpmock.NewBytesResponse(200, []byte(`{}`)), nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{
		Url:     "http://localhost:8080",
		Headers: map[string]string{"Authorization": "Bearer config-token"},
	})
	req := protocol.NewUpstreamRestRequest(
		"1",
		"POST#/exchange",
		&protocol.RequestParams{
			Headers: map[string][]string{"Authorization": {"Bearer client-attacker"}},
		},
		nil, "",
	)

	r := connector.SendRequest(context.Background(), req)

	require.False(t, r.HasError())
	assert.Equal(t, "Bearer config-token", gotHeader,
		"client-supplied headers must NOT override connector-config auth headers")
}

// REST treats anything in 2xx as success - 200, 201, 204 etc. Earlier
// implementation strictly checked == 200 and mis-classified everything else
// as an error.
func TestRestRequest_AllTwoHundredCodesAreSuccess(t *testing.T) {
	for _, code := range []int{200, 201, 202, 204, 299} {
		t.Run(http.StatusText(code), func(te *testing.T) {
			httpmock.Activate(te)
			defer httpmock.Deactivate()

			httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
				func(req *http.Request) (*http.Response, error) {
					return httpmock.NewBytesResponse(code, []byte(`{"ok":true}`)), nil
				})

			connector := newRestConnector(te, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
			req := protocol.NewUpstreamRestRequest("1", "POST#/exchange", nil, nil, "")

			r := connector.SendRequest(context.Background(), req)

			assert.False(te, r.HasError(), "HTTP %d must be treated as success", code)
			assert.Equal(te, code, r.ResponseCode())
		})
	}
}

// Structured {"code":N,"message":"..."} body wins on non-2xx - the
// upstream's own code/message reach the caller.
func TestRestRequest_StructuredErrorBodyIsUsedVerbatim(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			body := []byte(`{"code":400,"message":"BAD_REQUEST: missing Content-Type header"}`)
			return httpmock.NewBytesResponse(400, body), nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
	req := protocol.NewUpstreamRestRequest("1", "POST#/exchange", nil, nil, "")

	r := connector.SendRequest(context.Background(), req)

	require.True(t, r.HasError())
	assert.Equal(t, 400, r.GetError().Code)
	assert.Equal(t, "BAD_REQUEST: missing Content-Type header", r.GetError().Message)
}

// Plain-text non-2xx body - HTTP status becomes the code, body becomes the
// message verbatim within the truncation cap.
func TestRestRequest_NonJsonErrorBodyUsesHttpCode(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			return httpmock.NewBytesResponse(503, []byte("Service Unavailable")), nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
	req := protocol.NewUpstreamRestRequest("1", "POST#/exchange", nil, nil, "")

	r := connector.SendRequest(context.Background(), req)

	require.True(t, r.HasError())
	assert.Equal(t, 503, r.GetError().Code,
		"non-JSON error bodies must fall back to the HTTP status code")
	assert.Equal(t, "Service Unavailable", r.GetError().Message)
}

// Streaming REST requests get a streaming response without the JSON-RPC
// envelope peek - REST bodies are opaque pass-through.
func TestRestRequest_StreamRequestReturnsStreamingResponse(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	body := []byte(`{"hello":"world"}`)
	httpmock.RegisterResponder("GET", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			return httpmock.NewBytesResponse(200, body), nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
	streamReq := protocol.NewStreamUpstreamRestRequest("1", "GET#/status", nil, nil, "")

	r := connector.SendRequest(context.Background(), streamReq)

	require.False(t, r.HasError())
	assert.True(t, r.HasStream(),
		"REST streaming requests must always come back as streams - no envelope peek")
}

// REST responses must carry upstream headers (Content-Type, signatures,
// CORS, ...) back via the HasResponseHeaders interface so the HTTP-server
// layer can copy them onto the client response.
func TestRestRequest_UpstreamHeadersAttachedToResponse(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			resp := httpmock.NewBytesResponse(200, []byte(`{}`))
			resp.Header.Set("X-Trace-Id", "trace-1")
			resp.Header.Add("X-Multi", "one")
			resp.Header.Add("X-Multi", "two")
			return resp, nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
	req := protocol.NewUpstreamRestRequest("1", "POST#/exchange", nil, nil, "")

	r := connector.SendRequest(context.Background(), req)

	require.False(t, r.HasError())
	carrier, ok := r.(protocol.HasResponseHeaders)
	require.True(t, ok, "REST response must implement HasResponseHeaders")
	assert.Equal(t, "trace-1", carrier.ResponseHeaders().Get("X-Trace-Id"))
	assert.Equal(t, []string{"one", "two"}, carrier.ResponseHeaders().Values("X-Multi"),
		"multi-valued upstream headers must reach the client response")
}

// REST connector type-asserts on *UpstreamRestRequest. A non-REST request
// reaching the REST send path is a wiring bug - report it cleanly instead
// of crashing.
func TestRestConnector_RejectsNonRestRequest(t *testing.T) {
	connector := newRestConnector(t, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
	jsonRpcReq, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)

	r := connector.SendRequest(context.Background(), jsonRpcReq)

	require.True(t, r.HasError(),
		"feeding a JSON-RPC request to the REST connector must surface a client error")
	assert.Equal(t, protocol.ClientErrorCode, r.GetError().Code)
}
