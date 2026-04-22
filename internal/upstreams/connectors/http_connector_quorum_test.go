package connectors_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHttpConnector_ForwardsQuorumParamsAndCapturesHeaders(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	var gotQuery string
	body := []byte(`{"id":1,"jsonrpc":"2.0","result":"0x1"}`)
	httpmock.RegisterResponder("POST", "", func(req *http.Request) (*http.Response, error) {
		gotQuery = req.URL.RawQuery
		resp := httpmock.NewBytesResponse(200, body)
		resp.Header.Set("QR0-id-abc", "drpc-core@US-West#1(node-a)_nonce_1234_sig_0xabcd")
		resp.Header.Set("QR1-id-abc", "drpc-core@US-Central#1(node-b)_nonce_3456_sig_0xbcde")
		resp.Header.Set("X-Other", "not-a-qr-header")
		return resp, nil
	})

	cfg := &config.ApiConnectorConfig{Url: "http://localhost:8080"}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, protocol.JsonRpcConnector, "")
	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)

	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 2, QuorumOf: 3})
	r := connector.SendRequest(ctx, req)

	require.False(t, r.HasError(), "got unexpected error: %+v", r)
	assert.Contains(t, gotQuery, "quorum=3")
	assert.Contains(t, gotQuery, "quorum_required=2")

	hdr, ok := r.(protocol.HasResponseHeaders)
	require.True(t, ok)
	require.NotNil(t, hdr.ResponseHeaders())
	assert.Equal(t, "drpc-core@US-West#1(node-a)_nonce_1234_sig_0xabcd", hdr.ResponseHeaders().Get("QR0-id-abc"))
	assert.Equal(t, "not-a-qr-header", hdr.ResponseHeaders().Get("X-Other"))
}

func TestHttpConnector_QuorumForcesUnary_EvenForStreamRequest(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	// Large enough body that the streaming path would normally pick it up.
	body := []byte(`{"id":1,"jsonrpc":"2.0","result":"0x` + strings.Repeat("ab", 8192) + `"}`)
	httpmock.RegisterResponder("POST", "", func(req *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(200, body)
		resp.Header.Set("QR0-id-abc", "provider(node-0)_nonce_1_sig_0xaa")
		return resp, nil
	})

	cfg := &config.ApiConnectorConfig{Url: "http://localhost:8080"}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, protocol.JsonRpcConnector, "")
	streamReq := protocol.NewStreamUpstreamJsonRpcRequest("1", []byte(`"1"`), "eth_getLogs", nil, nil)

	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 1, QuorumOf: 1})
	r := connector.SendRequest(ctx, streamReq)

	require.False(t, r.HasError(), "got unexpected error: %+v", r)
	assert.False(t, r.HasStream(), "quorum in ctx must force unary (buffered) responses")

	hdr, ok := r.(protocol.HasResponseHeaders)
	require.True(t, ok)
	assert.Equal(t, "provider(node-0)_nonce_1_sig_0xaa", hdr.ResponseHeaders().Get("QR0-id-abc"))
}

func TestHttpConnector_NoQuorum_StreamRequestStaysStreamed(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	body := []byte(`[` + strings.Repeat(`{"x":1},`, 2048) + `{"x":1}]`)
	httpmock.RegisterResponder("POST", "", func(req *http.Request) (*http.Response, error) {
		return httpmock.NewBytesResponse(200, body), nil
	})

	cfg := &config.ApiConnectorConfig{Url: "http://localhost:8080"}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, protocol.JsonRpcConnector, "")
	streamReq := protocol.NewStreamUpstreamJsonRpcRequest("1", []byte(`"1"`), "eth_getLogs", nil, nil)

	r := connector.SendRequest(context.Background(), streamReq)
	require.False(t, r.HasError())
	assert.True(t, r.HasStream(), "no quorum ctx -> streaming path is taken as before")
}

func TestHttpConnector_MergesQuorumParamsWithExistingQuery(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	var gotURL string
	httpmock.RegisterResponder("POST", "=~^http://localhost:8080/rpc",
		func(req *http.Request) (*http.Response, error) {
			gotURL = req.URL.String()
			return httpmock.NewBytesResponse(200, []byte(`{"id":1,"jsonrpc":"2.0","result":"0x1"}`)), nil
		})

	cfg := &config.ApiConnectorConfig{Url: "http://localhost:8080/rpc?apikey=abc"}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, protocol.JsonRpcConnector, "")
	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)

	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 2, QuorumOf: 3})
	r := connector.SendRequest(ctx, req)
	require.False(t, r.HasError(), "got unexpected error: %+v", r)

	parsedURL, err := url.Parse(gotURL)
	require.NoError(t, err)
	q := parsedURL.Query()
	assert.Equal(t, "abc", q.Get("apikey"))
	assert.Equal(t, "3", q.Get("quorum"))
	assert.Equal(t, "2", q.Get("quorum_required"))
}

func TestHttpConnector_NoQuorumParams_NoQueryAppended(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	var gotQuery string
	httpmock.RegisterResponder("POST", "", func(req *http.Request) (*http.Response, error) {
		gotQuery = req.URL.RawQuery
		return httpmock.NewBytesResponse(200, []byte(`{"id":1,"jsonrpc":"2.0","result":"0x1"}`)), nil
	})

	cfg := &config.ApiConnectorConfig{Url: "http://localhost:8080"}
	connector := connectors.NewHttpConnectorWithDefaultClient(cfg, protocol.JsonRpcConnector, "")
	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)

	_ = connector.SendRequest(context.Background(), req)

	assert.Empty(t, gotQuery)
}
