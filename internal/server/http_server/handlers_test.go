package http_server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/http_server"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain loads a dedicated test_specs/ fixture so the REST parser tests
// can exercise wildcard / multi-verb / literal-beats-wildcard cases without
// depending on whatever the embedded production specs happen to declare.
// The spec name registered there is "rest-test".
func TestMain(m *testing.M) {
	if err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs")).Load(); err != nil {
		panic("failed to load method specs in test setup: " + err.Error())
	}
	os.Exit(m.Run())
}

// newRestReq builds an *http.Request the way echo would hand it to us:
// the path on the request mirrors what the client sent, body is provided
// inline.
func newRestReq(t *testing.T, method, urlStr string, body io.Reader) *http.Request {
	t.Helper()
	return httptest.NewRequest(method, urlStr, body)
}

// TestRestHandlerAcceptsEmptyBody is the regression test for the "couldn't
// parse a request" bug: every REST GET arrived with an empty body, but the
// old NewRestHandler ran sonic.Valid([]byte{}) which is false, so it always
// short-circuited with parse error.
func TestRestHandlerAcceptsEmptyBody(t *testing.T) {
	handler, err := http_server.NewRestHandler(
		&server_ctx.Request{Chain: "hyperliquid"},
		newRestReq(t, "POST", "/exchange", nil),
		"exchange",
	)

	assert.NoError(t, err, "empty body must not be rejected for REST requests")
	assert.NotNil(t, handler)
	assert.True(t, handler.IsSingle())
	assert.Equal(t, 1, handler.RequestCount())
	assert.Equal(t, protocol.Rest, handler.GetRequestType())
}

func TestRestHandlerAcceptsValidJsonBody(t *testing.T) {
	handler, err := http_server.NewRestHandler(
		&server_ctx.Request{Chain: "hyperliquid"},
		newRestReq(t, "POST", "/exchange", strings.NewReader(`{"raw":"AAA"}`)),
		"exchange",
	)

	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestRestHandlerRejectsMalformedJsonBody(t *testing.T) {
	_, err := http_server.NewRestHandler(
		&server_ctx.Request{Chain: "hyperliquid"},
		newRestReq(t, "POST", "/exchange", strings.NewReader(`{not json`)),
		"exchange",
	)

	assert.Error(t, err, "non-empty bodies must still be validated as JSON")
}

func TestRestHandlerRequestDecodePopulatesMatchedTemplate(t *testing.T) {
	handler, err := http_server.NewRestHandler(
		&server_ctx.Request{Chain: "hyperliquid"},
		newRestReq(t, "POST", "/exchange", nil),
		"exchange",
	)
	require.NoError(t, err)

	request, err := handler.RequestDecode(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "hyperliquid", request.Chain)
	require.Len(t, request.UpstreamRequests, 1)

	up := request.UpstreamRequests[0]
	assert.Equal(t, "POST"+protocol.MethodSeparator+"/exchange", up.Method(),
		"matched template becomes the canonical method - here it's a literal template")
	assert.Equal(t, protocol.Rest, up.RequestType())
	body, err := up.Body()
	assert.NoError(t, err)
	assert.Empty(t, body)
}

func TestRestHandlerRequestDecodeForwardsBody(t *testing.T) {
	payload := `{"raw":"AAA"}`
	handler, err := http_server.NewRestHandler(
		&server_ctx.Request{Chain: "hyperliquid"},
		newRestReq(t, "POST", "/exchange", strings.NewReader(payload)),
		"exchange",
	)
	require.NoError(t, err)

	request, err := handler.RequestDecode(context.Background())
	require.NoError(t, err)
	require.Len(t, request.UpstreamRequests, 1)

	up := request.UpstreamRequests[0]
	assert.Equal(t, "POST"+protocol.MethodSeparator+"/exchange", up.Method())
	body, err := up.Body()
	assert.NoError(t, err)
	assert.Equal(t, []byte(payload), body)
}

func TestRestHandlerPromotesQueryAndHeadersIntoRequestParams(t *testing.T) {
	httpReq := newRestReq(t, "POST", "/exchange?token=A&token=B&quorum=3", nil)
	httpReq.Header.Set("X-Custom", "hello")
	httpReq.Header.Add("X-Multi", "one")
	httpReq.Header.Add("X-Multi", "two")

	handler, err := http_server.NewRestHandler(
		&server_ctx.Request{Chain: "hyperliquid"},
		httpReq,
		"exchange",
	)
	require.NoError(t, err)

	request, err := handler.RequestDecode(context.Background())
	require.NoError(t, err)
	up := request.UpstreamRequests[0].(*protocol.UpstreamRestRequest)
	rp := up.RequestParams()
	require.NotNil(t, rp)

	assert.NotContains(t, rp.QueryParams, "quorum",
		"nodecore-reserved query params must be stripped before forwarding")
	assert.Equal(t, []string{"A", "B"}, rp.QueryParams["token"],
		"repeated query values must survive the round-trip")

	assert.Equal(t, []string{"hello"}, rp.Headers["X-Custom"])
	assert.Equal(t, []string{"one", "two"}, rp.Headers["X-Multi"],
		"repeated header values must survive the round-trip")
}

// The two method-rejection cases live in handlers_utf8_test.go (internal test
// package) so they can assert the sentinel with errors.Is. The accept-side cases
// below assert no error, so they stay here.

// Valid multi-byte UTF-8 is not invalid UTF-8. Only malformed byte sequences are
// rejected, so a non-ASCII but well-formed name must pass.
func TestJsonRpcHandlerAcceptsMultiByteUtf8Method(t *testing.T) {
	body := `{"id":1,"jsonrpc":"2.0","method":"eth_日本語","params":[]}`

	handler, err := http_server.NewJsonRpcHandler(
		&server_ctx.Request{Chain: "ethereum"},
		strings.NewReader(body),
		false,
	)

	require.NoError(t, err, "well-formed multi-byte UTF-8 must not be rejected")
	assert.NotNil(t, handler)
}

// The rule is method-only. Junk bytes in params never become a method name or a
// metric label, so they must flow through untouched.
func TestJsonRpcHandlerAcceptsNonUtf8Params(t *testing.T) {
	body := "{\"id\":1,\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[\"\xff\"]}"

	handler, err := http_server.NewJsonRpcHandler(
		&server_ctx.Request{Chain: "ethereum"},
		strings.NewReader(body),
		false,
	)

	require.NoError(t, err, "invalid bytes outside the method name must not reject the request")
	assert.NotNil(t, handler)
}

// Horizon's POST /transactions is application/x-www-form-urlencoded
// (tx=<base64 XDR>). That body is not JSON and must still reach the upstream.
func TestRestHandlerAcceptsFormUrlencodedBody(t *testing.T) {
	req := newRestReq(t, "POST", "/transactions", strings.NewReader("tx=AAAAAgAAAA"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	handler, err := http_server.NewRestHandler(&server_ctx.Request{Chain: "hyperliquid"}, req, "transactions")

	assert.NoError(t, err, "a declared non-JSON body must pass through opaquely")
	assert.NotNil(t, handler)
}

func TestRestHandlerStillRejectsMalformedDeclaredJsonBody(t *testing.T) {
	req := newRestReq(t, "POST", "/exchange", strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/json")

	_, err := http_server.NewRestHandler(&server_ctx.Request{Chain: "hyperliquid"}, req, "exchange")

	assert.Error(t, err, "a body the client says is JSON must still be validated")
}

// A JSON suffix type (application/problem+json, application/vnd.api+json) is
// still JSON and must still be validated.
func TestRestHandlerRejectsMalformedJsonSuffixBody(t *testing.T) {
	req := newRestReq(t, "POST", "/exchange", strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/vnd.api+json; charset=utf-8")

	_, err := http_server.NewRestHandler(&server_ctx.Request{Chain: "hyperliquid"}, req, "exchange")

	assert.Error(t, err)
}

// With no Content-Type at all we keep the old strict behavior: an undeclared
// body is assumed to be JSON.
func TestRestHandlerRejectsMalformedUndeclaredBody(t *testing.T) {
	_, err := http_server.NewRestHandler(
		&server_ctx.Request{Chain: "hyperliquid"},
		newRestReq(t, "POST", "/exchange", strings.NewReader(`{not json`)),
		"exchange",
	)

	assert.Error(t, err)
}
