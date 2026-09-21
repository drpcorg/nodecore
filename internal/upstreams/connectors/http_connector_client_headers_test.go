package connectors_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureHeaders serves any request, records the Content-Type values exactly as
// they arrived on the wire, and answers with an empty JSON object.
func captureHeaders(t *testing.T, got *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = append([]string(nil), r.Header.Values("Content-Type")...)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
}

func restRequestWithHeaders(headers map[string][]string, body []byte) protocol.RequestHolder {
	return protocol.NewUpstreamRestRequest(
		"1",
		"POST#/transactions",
		&protocol.RequestParams{Headers: headers},
		body,
		chains.GetMethodSpecNameByChainName("stellar"),
	)
}

// Content-Type is a singleton field: a client-declared value must REPLACE the
// connector's application/json default, not stack behind it. Upstreams read the
// first value, so stacking makes a form body look like JSON.
func TestClientContentTypeReplacesTheDefault(t *testing.T) {
	var got []string
	ts := captureHeaders(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL, Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), restRequestWithHeaders(
		map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}},
		[]byte("tx=AAAAAgAAAA"),
	))
	require.False(t, response.HasError())

	assert.Equal(t, []string{"application/x-www-form-urlencoded"}, got)
}

// The common case: a client that agrees with the default must not produce two
// identical (and malformed) Content-Type lines.
func TestClientJsonContentTypeIsNotDuplicated(t *testing.T) {
	var got []string
	ts := captureHeaders(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL, Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), restRequestWithHeaders(
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"a":1}`),
	))
	require.False(t, response.HasError())

	assert.Equal(t, []string{"application/json"}, got)
}

func TestNoClientContentTypeKeepsTheJsonDefault(t *testing.T) {
	var got []string
	ts := captureHeaders(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL, Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), restRequestWithHeaders(nil, []byte(`{"a":1}`)))
	require.False(t, response.HasError())

	assert.Equal(t, []string{"application/json"}, got)
}

// A Content-Type pinned in the connector config still wins: the client must not
// be able to override connector-owned headers.
func TestConfiguredContentTypeBeatsTheClient(t *testing.T) {
	var got []string
	ts := captureHeaders(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{
			Url:     ts.URL,
			Type:    "rest",
			Headers: map[string]string{"Content-Type": "application/json"},
		}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), restRequestWithHeaders(
		map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}},
		[]byte("tx=AAAAAgAAAA"),
	))
	require.False(t, response.HasError())

	assert.Equal(t, []string{"application/json"}, got)
}

// Non-singleton headers keep stacking.
func TestOtherClientHeadersStillStack(t *testing.T) {
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append([]string(nil), r.Header.Values("X-Custom-Header")...)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL, Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), restRequestWithHeaders(
		map[string][]string{"X-Custom-Header": {"alpha", "beta"}}, nil))
	require.False(t, response.HasError())

	assert.Equal(t, []string{"alpha", "beta"}, got)
}

// capturePath records the request path exactly as it arrived on the wire.
func capturePath(t *testing.T, got *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
}

func rootRequest() protocol.RequestHolder {
	return protocol.NewInternalUpstreamRestRequest("GET#/", nil, chains.GetChain("stellar").Chain)
}

// A connector URL with a trailing slash plus the root template "GET#/" must not
// send "GET //" upstream: Horizon's root probe (head, passphrase, version,
// history boundary) is exactly this request, and "//" is a different path.
func TestRootPathIsNotDoubledForATrailingSlashEndpoint(t *testing.T) {
	var got string
	ts := capturePath(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL + "/", Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), rootRequest())
	require.False(t, response.HasError())

	assert.Equal(t, "/", got)
}

func TestRootPathIsKeptForASlashlessEndpoint(t *testing.T) {
	var got string
	ts := capturePath(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL, Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), rootRequest())
	require.False(t, response.HasError())

	assert.Equal(t, "/", got)
}

// The same collapse must apply to deeper templates - a trailing-slash endpoint
// with GET#/health has always produced "//health".
func TestNestedPathIsNotDoubledForATrailingSlashEndpoint(t *testing.T) {
	var got string
	ts := capturePath(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL + "/", Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(),
		protocol.NewInternalUpstreamRestRequest("GET#/health", nil, chains.GetChain("stellar").Chain))
	require.False(t, response.HasError())

	assert.Equal(t, "/health", got)
}

// A base path on the endpoint must survive: /api + /health is /api/health.
func TestEndpointBasePathIsPreserved(t *testing.T) {
	var got string
	ts := capturePath(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL + "/api", Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(),
		protocol.NewInternalUpstreamRestRequest("GET#/health", nil, chains.GetChain("stellar").Chain))
	require.False(t, response.HasError())

	assert.Equal(t, "/api/health", got)
}

// The regression this guard exists for: `curl -d '{"...}"'` sends
// application/x-www-form-urlencoded by default, and a browser `fetch` with a
// JSON.stringify body sends text/plain - neither chosen by the developer. Both
// carry a JSON body, and grpc-gateway (Cosmos LCD) / Aptos answer 415 for a
// non-JSON content type, so nodecore's application/json default must keep
// winning for them the way it did before Horizon needed passthrough.
func TestJsonBodyKeepsTheJsonDefaultDespiteAFormContentType(t *testing.T) {
	var got []string
	ts := captureHeaders(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL, Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), restRequestWithHeaders(
		map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}},
		[]byte(`{"tx_bytes":"Cr4BCrsBChwvY29z","mode":"BROADCAST_MODE_SYNC"}`),
	))
	require.False(t, response.HasError())

	assert.Equal(t, []string{"application/json"}, got)
}

func TestJsonBodyKeepsTheJsonDefaultDespiteATextPlainContentType(t *testing.T) {
	var got []string
	ts := captureHeaders(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL, Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), restRequestWithHeaders(
		map[string][]string{"Content-Type": {"text/plain;charset=UTF-8"}},
		[]byte(`{"a":1}`),
	))
	require.False(t, response.HasError())

	assert.Equal(t, []string{"application/json"}, got)
}

// A body that is NOT JSON leaves the default as a provably wrong guess, so the
// client's declaration is the only information available - Horizon's
// POST /transactions is exactly this.
func TestNonJsonBodyLetsTheClientContentTypeThrough(t *testing.T) {
	var got []string
	ts := captureHeaders(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL, Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), restRequestWithHeaders(
		map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}},
		[]byte("tx=AAAAAgAAAABmZm9vAAAAZAAA"),
	))
	require.False(t, response.HasError())

	assert.Equal(t, []string{"application/x-www-form-urlencoded"}, got)
}

// A client declaring a JSON type needs no body inspection at all: replacing the
// default with an equivalent value is a no-op, and skipping the scan keeps the
// common path free (the handler already validated that body).
func TestJsonSuffixContentTypeIsForwardedWithoutBodyInspection(t *testing.T) {
	var got []string
	ts := captureHeaders(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL, Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), restRequestWithHeaders(
		map[string][]string{"Content-Type": {"application/vnd.api+json"}},
		[]byte(`{"a":1}`),
	))
	require.False(t, response.HasError())

	assert.Equal(t, []string{"application/vnd.api+json"}, got)
}

// An empty body carries no evidence either way; the declaration stands.
func TestEmptyBodyLetsTheClientContentTypeThrough(t *testing.T) {
	var got []string
	ts := captureHeaders(t, &got)
	defer ts.Close()

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: ts.URL, Type: "rest"}, specs.RestConnector, "", "id")
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), restRequestWithHeaders(
		map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}}, nil))
	require.False(t, response.HasError())

	assert.Equal(t, []string{"application/x-www-form-urlencoded"}, got)
}
