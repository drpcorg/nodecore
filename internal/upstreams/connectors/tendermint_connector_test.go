package connectors_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturedRequest records what the fake CometBFT endpoint actually received.
type capturedRequest struct {
	method string
	path   string
	query  string
	body   string
}

func newTendermintConnector(t *testing.T, handler http.HandlerFunc) (*connectors.HttpConnector, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.query = r.URL.RawQuery
		captured.body = string(body)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	connector, err := connectors.NewHttpConnector(
		&config.ApiConnectorConfig{Url: srv.URL},
		specs.TendermintConnector,
		"",
		"test-upstream",
	)
	require.NoError(t, err)
	return connector, captured
}

// A JSON-RPC request is POSTed to the endpoint root with the body verbatim,
// and the shared JSON-RPC response path unwraps CometBFT's result envelope.
func TestTendermintConnectorSendsJsonRpcShape(t *testing.T) {
	connector, captured := newTendermintConnector(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"sync_info":{"latest_block_height":"25000000"}}}`))
	})

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("status", map[string]any{}, chains.COSMOS_HUB)
	require.NoError(t, err)

	response := connector.SendRequest(context.Background(), request)

	require.False(t, response.HasError())
	assert.JSONEq(t, `{"sync_info":{"latest_block_height":"25000000"}}`, string(response.ResponseResult()))

	assert.Equal(t, http.MethodPost, captured.method)
	assert.Equal(t, "/", captured.path)
	assert.Empty(t, captured.query)
	assert.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"status","params":{}}`, captured.body)
}

// The same connector serves a URI-style request as GET /<method>?<args>, which
// is how a REST client reaches the very same tendermint methods.
func TestTendermintConnectorSendsRestShape(t *testing.T) {
	connector, captured := newTendermintConnector(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":-1,"result":{"block_id":{"hash":"AABB"}}}`))
	})

	request := protocol.NewUpstreamRestRequest(
		"1",
		"GET#/block",
		&protocol.RequestParams{QueryParams: map[string][]string{"height": {"25000000"}}},
		nil,
		"cosmos",
	)

	response := connector.SendRequest(context.Background(), request)

	require.False(t, response.HasError())
	// REST bodies are opaque pass-through, so the client sees the envelope the
	// node produced - exactly what a direct call to 26657 returns.
	assert.JSONEq(t,
		`{"jsonrpc":"2.0","id":-1,"result":{"block_id":{"hash":"AABB"}}}`,
		string(response.ResponseResult()),
	)

	assert.Equal(t, http.MethodGet, captured.method)
	assert.Equal(t, "/block", captured.path)
	assert.Equal(t, "height=25000000", captured.query)
	assert.Empty(t, captured.body)
}

// Path wildcards still expand, so a tendermint spec is free to use them.
func TestTendermintConnectorExpandsRestPathParams(t *testing.T) {
	connector, captured := newTendermintConnector(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})

	request := protocol.NewUpstreamRestRequest(
		"1",
		"GET#/tx/*",
		&protocol.RequestParams{PathParams: []string{"0xdeadbeef"}},
		nil,
		"cosmos",
	)

	response := connector.SendRequest(context.Background(), request)

	require.False(t, response.HasError())
	assert.Equal(t, "/tx/0xdeadbeef", captured.path)
}

func TestTendermintConnectorReportsType(t *testing.T) {
	connector, _ := newTendermintConnector(t, func(w http.ResponseWriter, _ *http.Request) {})
	assert.Equal(t, specs.TendermintConnector, connector.GetType())
}
