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
		&http_server.Request{Chain: "hyperliquid"},
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
		&http_server.Request{Chain: "hyperliquid"},
		newRestReq(t, "POST", "/exchange", strings.NewReader(`{"raw":"AAA"}`)),
		"exchange",
	)

	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestRestHandlerRejectsMalformedJsonBody(t *testing.T) {
	_, err := http_server.NewRestHandler(
		&http_server.Request{Chain: "hyperliquid"},
		newRestReq(t, "POST", "/exchange", strings.NewReader(`{not json`)),
		"exchange",
	)

	assert.Error(t, err, "non-empty bodies must still be validated as JSON")
}

func TestRestHandlerRequestDecodePopulatesMatchedTemplate(t *testing.T) {
	handler, err := http_server.NewRestHandler(
		&http_server.Request{Chain: "hyperliquid"},
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
		&http_server.Request{Chain: "hyperliquid"},
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
		&http_server.Request{Chain: "hyperliquid"},
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
