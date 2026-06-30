package connectors_test

import (
	"context"
	"encoding/json"
	"errors"
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

func TestJsonRpcRequest200CodeThenNoStream(t *testing.T) {
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

	assert.False(t, r.HasStream())
	assert.False(t, r.HasError())
	assert.Equal(t, []byte(`{"number": "0x11"}`), r.ResponseResult())
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

// TestUpstreamUrlAndHostNotLeakedOnConnectionFailure guards against leaking
// upstream infrastructure to the caller when the upstream connection fails.
// http.Client.Do returns a *url.Error whose Error() embeds the full URL
// (including any API key in its path/query and the host itself). The
// client-facing error must reference the upstream by its configured id only;
// neither the URL/key nor the host may appear in it.
func TestUpstreamUrlAndHostNotLeakedOnConnectionFailure(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterResponder("POST", "", func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})

	cfg := &config.ApiConnectorConfig{
		Url: "https://eth-mainnet.example.com/v2/SUPER_SECRET_KEY",
	}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, specs.JsonRpcConnector, "").
		WithUpstreamId("my-upstream-id")
	jsonBody := protocol.JsonRpcRequestBody{Id: json.RawMessage(`"real"`), Method: "eth_test", Params: nil}
	req := protocol.NewStreamUpstreamJsonRpcRequest("id", jsonBody, "")

	r := connector.SendRequest(context.Background(), req)

	require.True(t, r.HasError())
	msg := r.GetError().Message
	assert.NotContains(t, msg, "SUPER_SECRET_KEY", "upstream API key must not reach the client")
	assert.NotContains(t, msg, "eth-mainnet.example.com", "upstream host (infra) must not reach the client")
	assert.Contains(t, msg, "my-upstream-id", "client error should reference the upstream by its id")
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

	var gotValues []string
	httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			gotValues = req.Header.Values("Authorization")
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
	assert.Equal(t, []string{"Bearer config-token"}, gotValues,
		"client-supplied headers must NOT override connector-config auth headers, "+
			"and there must be exactly one value on the wire (no smuggled second slot)")
}

// HTTP headers are case-insensitive. A client sending "authorization"
// must be rejected against a config "Authorization" - if the map lookup
// were case-sensitive, both values would end up canonicalised into the
// same slot by req.Header.Add and leak the client-supplied token onto the
// wire alongside the trusted config value.
func TestRestRequest_ConfigHeadersWinAcrossCasing(t *testing.T) {
	cases := []struct {
		name        string
		configKey   string
		clientKey   string
		configValue string
		clientValue string
	}{
		{"config-canonical-client-lowercase", "Authorization", "authorization", "Bearer config", "Bearer attacker"},
		{"config-lowercase-client-canonical", "authorization", "Authorization", "Bearer config", "Bearer attacker"},
		{"config-screaming-client-canonical", "AUTHORIZATION", "Authorization", "Bearer config", "Bearer attacker"},
		{"config-mixed-client-mixed", "AuThOrIzAtIoN", "aUtHoRiZaTiOn", "Bearer config", "Bearer attacker"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(te *testing.T) {
			httpmock.Activate(te)
			defer httpmock.Deactivate()

			var gotValues []string
			httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
				func(req *http.Request) (*http.Response, error) {
					gotValues = req.Header.Values("Authorization")
					return httpmock.NewBytesResponse(200, []byte(`{}`)), nil
				})

			connector := newRestConnector(te, &config.ApiConnectorConfig{
				Url:     "http://localhost:8080",
				Headers: map[string]string{tc.configKey: tc.configValue},
			})
			req := protocol.NewUpstreamRestRequest(
				"1",
				"POST#/exchange",
				&protocol.RequestParams{
					Headers: map[string][]string{tc.clientKey: {tc.clientValue}},
				},
				nil, "",
			)

			r := connector.SendRequest(context.Background(), req)
			require.False(te, r.HasError())
			assert.Equal(te, []string{tc.configValue}, gotValues,
				"config %q must win across casing, with no second value smuggled in by client %q",
				tc.configKey, tc.clientKey)
		})
	}
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

// Stream-gate must match the parser's 2xx success window - a 201 or 204
// upstream reply on a streaming request must come back as a stream, not
// silently fall through to the buffered path because the gate was pinned
// to == 200.
func TestRestRequest_StreamReturnsStreamingResponseAcrossAllTwoHundredCodes(t *testing.T) {
	for _, code := range []int{200, 201, 202, 204, 299} {
		t.Run(http.StatusText(code), func(te *testing.T) {
			httpmock.Activate(te)
			defer httpmock.Deactivate()

			httpmock.RegisterResponder("GET", "=~^http://localhost:8080/.*",
				func(req *http.Request) (*http.Response, error) {
					return httpmock.NewBytesResponse(code, []byte(`{"hello":"world"}`)), nil
				})

			connector := newRestConnector(te, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
			streamReq := protocol.NewStreamUpstreamRestRequest("1", "GET#/status", nil, nil, "")

			r := connector.SendRequest(context.Background(), streamReq)
			require.False(te, r.HasError(), "HTTP %d must be a success", code)
			assert.True(te, r.HasStream(),
				"HTTP %d must produce a streaming response (gate must mirror the 2xx success window)", code)
		})
	}
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

// Default response-header deny list strips RFC 7230 hop-by-hop headers,
// Set-Cookie (would leak upstream session state to the client), and
// Server (would fingerprint the backend). Anything not in the list
// passes through unchanged.
func TestRestRequest_DefaultResponseHeaderDenyList(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			resp := httpmock.NewBytesResponse(200, []byte(`{}`))
			// Default-denied
			resp.Header.Set("Set-Cookie", "session=abc; HttpOnly")
			resp.Header.Set("Server", "nginx/1.21")
			resp.Header.Set("Connection", "keep-alive")
			resp.Header.Set("Transfer-Encoding", "chunked")
			// Should pass through
			resp.Header.Set("X-Trace-Id", "trace-1")
			resp.Header.Set("Content-Type", "application/json")
			return resp, nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{Url: "http://localhost:8080"})
	req := protocol.NewUpstreamRestRequest("1", "POST#/exchange", nil, nil, "")

	r := connector.SendRequest(context.Background(), req)
	require.False(t, r.HasError())
	carrier, ok := r.(protocol.HasResponseHeaders)
	require.True(t, ok)
	hdr := carrier.ResponseHeaders()

	assert.Empty(t, hdr.Values("Set-Cookie"),
		"Set-Cookie must be stripped to avoid leaking upstream sessions")
	assert.Empty(t, hdr.Values("Server"),
		"Server must be stripped so the upstream isn't fingerprinted")
	assert.Empty(t, hdr.Values("Connection"),
		"RFC 7230 hop-by-hop: Connection must not be forwarded by a proxy")
	assert.Empty(t, hdr.Values("Transfer-Encoding"),
		"RFC 7230 hop-by-hop: Transfer-Encoding must not be forwarded by a proxy")

	assert.Equal(t, "trace-1", hdr.Get("X-Trace-Id"),
		"non-denied headers must pass through unchanged")
	assert.Equal(t, "application/json", hdr.Get("Content-Type"))
}

// Operator-supplied entries extend the default deny list. Matching is
// case-insensitive: a config "x-internal-route" entry must strip the
// upstream's "X-Internal-Route" header.
func TestRestRequest_ConfigExtendsResponseHeaderDenyList(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterResponder("POST", "=~^http://localhost:8080/.*",
		func(req *http.Request) (*http.Response, error) {
			resp := httpmock.NewBytesResponse(200, []byte(`{}`))
			resp.Header.Set("X-Internal-Route", "ingress-1")
			resp.Header.Set("X-Trace-Id", "trace-1")
			return resp, nil
		})

	connector := newRestConnector(t, &config.ApiConnectorConfig{
		Url:                "http://localhost:8080",
		ResponseHeaderDeny: []string{"x-internal-route"},
	})
	req := protocol.NewUpstreamRestRequest("1", "POST#/exchange", nil, nil, "")

	r := connector.SendRequest(context.Background(), req)
	require.False(t, r.HasError())
	carrier, _ := r.(protocol.HasResponseHeaders)
	hdr := carrier.ResponseHeaders()

	assert.Empty(t, hdr.Values("X-Internal-Route"),
		"operator-supplied deny entries must strip the header (case-insensitive)")
	assert.Equal(t, "trace-1", hdr.Get("X-Trace-Id"),
		"non-denied headers must still pass through")
}

// JSON-RPC path goes through the same dispatch as REST, so the deny list
// applies equally - quorum-style QR<N> headers stay, Set-Cookie disappears.
func TestJsonRpc_ResponseHeaderDenyListApplies(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterResponder("POST", "",
		func(req *http.Request) (*http.Response, error) {
			resp := httpmock.NewBytesResponse(200, []byte(`{"id":1,"jsonrpc":"2.0","result":"0x1"}`))
			resp.Header.Set("Set-Cookie", "session=abc")
			resp.Header.Set("QR0-id-abc", "drpc-core_nonce_1_sig_0xaa")
			return resp, nil
		})

	cfg := &config.ApiConnectorConfig{Url: "http://localhost:8080"}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, specs.JsonRpcConnector, "")
	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)

	r := connector.SendRequest(context.Background(), req)
	require.False(t, r.HasError())
	carrier, ok := r.(protocol.HasResponseHeaders)
	require.True(t, ok)
	hdr := carrier.ResponseHeaders()

	assert.Empty(t, hdr.Values("Set-Cookie"),
		"deny list applies equally to JSON-RPC responses")
	assert.Equal(t, "drpc-core_nonce_1_sig_0xaa", hdr.Get("QR0-id-abc"),
		"quorum signature headers are not in the deny list and must survive")
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
