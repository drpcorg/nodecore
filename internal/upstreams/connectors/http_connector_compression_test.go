package connectors_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/methods"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var upstreamBody = []byte(`{"jsonrpc":"2.0","id":1,"result":{"number":"0x1337"}}`)

func encodeUpstream(t *testing.T, scheme string, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	switch scheme {
	case "gzip":
		w := gzip.NewWriter(&buf)
		_, err := w.Write(plain)
		require.NoError(t, err)
		require.NoError(t, w.Close())
	case "zstd":
		w, err := zstd.NewWriter(&buf)
		require.NoError(t, err)
		_, err = w.Write(plain)
		require.NoError(t, err)
		require.NoError(t, w.Close())
	default:
		return plain
	}
	return buf.Bytes()
}

// upstreamServing answers every request with plain encoded as scheme, and
// records the Accept-Encoding the connector offered.
func upstreamServing(t *testing.T, scheme string, plain []byte) (*httptest.Server, *string) {
	t.Helper()
	offered := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*offered = r.Header.Get("Accept-Encoding")
		if scheme != "" {
			w.Header().Set("Content-Encoding", scheme)
		}
		_, _ = w.Write(encodeUpstream(t, scheme, plain))
	}))
	t.Cleanup(srv.Close)
	return srv, offered
}

func restConnectorFor(t *testing.T, cfg *config.ApiConnectorConfig) *connectors.HttpConnector {
	t.Helper()
	connector, err := connectors.NewHttpConnector(cfg, specs.RestConnector, "", "test-upstream")
	require.NoError(t, err)
	return connector
}

// Go's transport only ever negotiates gzip on its own, so zstd has to be
// offered explicitly - which also hands nodecore the job of decoding both.
func TestUpstreamRequestOffersZstdAndGzip(t *testing.T) {
	srv, offered := upstreamServing(t, "", upstreamBody)
	connector := restConnectorFor(t, &config.ApiConnectorConfig{Url: srv.URL})

	r := connector.SendRequest(context.Background(), protocol.NewUpstreamRestRequest("1", "GET#/status", nil, nil, ""))

	require.False(t, r.HasError())
	assert.Equal(t, "zstd, gzip", *offered)
}

// Whatever coding the node answers with, the framework above the connector
// must see plain JSON: the connector strips Content-Encoding, so compressed
// bytes leaving here would reach the client unlabelled and unreadable.
func TestUpstreamResponseIsDecoded(t *testing.T) {
	for _, scheme := range []string{"zstd", "gzip", ""} {
		name := scheme
		if name == "" {
			name = "identity"
		}
		t.Run(name, func(te *testing.T) {
			srv, _ := upstreamServing(te, scheme, upstreamBody)
			connector := restConnectorFor(te, &config.ApiConnectorConfig{Url: srv.URL})

			r := connector.SendRequest(context.Background(), protocol.NewUpstreamRestRequest("1", "GET#/status", nil, nil, ""))

			require.False(te, r.HasError())
			assert.Equal(te, upstreamBody, r.ResponseResult())
			carrier, ok := r.(protocol.HasResponseHeaders)
			require.True(te, ok)
			assert.Empty(te, carrier.ResponseHeaders().Get("Content-Encoding"),
				"the body is plain now, so nothing may claim otherwise")
		})
	}
}

// The streaming path never buffers the body, so it needs the decoder wired
// into the stream itself rather than around a finished response.
func TestUpstreamStreamedResponseIsDecoded(t *testing.T) {
	for _, scheme := range []string{"zstd", "gzip"} {
		t.Run(scheme, func(te *testing.T) {
			plain := bytes.Repeat([]byte(`{"chunk":"0123456789"}`), 512)
			srv, _ := upstreamServing(te, scheme, plain)
			connector := restConnectorFor(te, &config.ApiConnectorConfig{Url: srv.URL})

			r := connector.SendRequest(
				context.Background(),
				protocol.NewStreamUpstreamRestRequest("1", "GET#/status", nil, nil, ""),
			)

			require.False(te, r.HasError())
			require.True(te, r.HasStream())
			got, err := io.ReadAll(r.EncodeResponse([]byte("1")))
			require.NoError(te, err)
			assert.Equal(te, plain, got)
		})
	}
}

// An operator who pins Accept-Encoding on the connector has a reason - a node
// that mishandles one of the codings, most likely - and the connector must
// not talk over them.
func TestConfiguredAcceptEncodingIsNotOverridden(t *testing.T) {
	srv, offered := upstreamServing(t, "gzip", upstreamBody)
	connector := restConnectorFor(t, &config.ApiConnectorConfig{
		Url:     srv.URL,
		Headers: map[string]string{"Accept-Encoding": "gzip"},
	})

	r := connector.SendRequest(context.Background(), protocol.NewUpstreamRestRequest("1", "GET#/status", nil, nil, ""))

	require.False(t, r.HasError())
	assert.Equal(t, "gzip", *offered)
	assert.Equal(t, upstreamBody, r.ResponseResult(),
		"a pinned coding must still be decoded")
}

// A node answering with a coding nodecore never offered has broken the
// negotiation. Failing is the only honest outcome: the bytes are undecodable
// here and would be unreadable at the client.
func TestUnsupportedUpstreamCodingFails(t *testing.T) {
	srv, _ := upstreamServing(t, "", upstreamBody)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "br")
		_, _ = w.Write(upstreamBody)
	})
	connector := restConnectorFor(t, &config.ApiConnectorConfig{Url: srv.URL})

	r := connector.SendRequest(context.Background(), protocol.NewUpstreamRestRequest("1", "GET#/status", nil, nil, ""))

	assert.True(t, r.HasError(), "an undecodable body must not be passed off as a result")
}
