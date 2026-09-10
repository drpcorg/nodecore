package connectors_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/compression"
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

// A node that puts a coding on a bodyless response - a 204, or an empty 200 -
// is describing bytes that are not there. Before zstd, Go's transparent gzip
// surfaced that as a clean empty body and the request succeeded; decoding it
// here must not turn it into a partial failure.
func TestUpstreamEmptyBodyWithAContentEncodingStillSucceeds(t *testing.T) {
	tests := []struct {
		name   string
		status int
		scheme string
	}{
		{"200 with an empty gzip body", http.StatusOK, "gzip"},
		{"200 with an empty zstd body", http.StatusOK, "zstd"},
		{"204 labelled gzip", http.StatusNoContent, "gzip"},
		{"204 labelled zstd", http.StatusNoContent, "zstd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", tt.scheme)
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			r := restConnectorFor(te, &config.ApiConnectorConfig{Url: srv.URL}).
				SendRequest(context.Background(), protocol.NewUpstreamRestRequest("1", "GET#/status", nil, nil, ""))

			require.False(te, r.HasError(), "an empty body is not an undecodable one")
			assert.Empty(te, r.ResponseResult())
		})
	}
}

// The window cap that protects the ingress protects this edge too: a node
// answering with a frame that demands more window than any real encoder emits
// is a node nodecore should refuse rather than allocate for.
func TestUpstreamFrameAboveTheWindowCapFails(t *testing.T) {
	var buf bytes.Buffer
	writer, err := zstd.NewWriter(&buf, zstd.WithWindowSize(64<<20), zstd.WithEncoderLevel(zstd.SpeedFastest))
	require.NoError(t, err)
	_, err = writer.Write(bytes.Repeat(upstreamBody, 1<<14))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "zstd")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	r := restConnectorFor(t, &config.ApiConnectorConfig{Url: srv.URL}).
		SendRequest(context.Background(), protocol.NewUpstreamRestRequest("1", "GET#/status", nil, nil, ""))

	assert.True(t, r.HasError())
}

// A streamed upstream response is torn down from the consuming goroutine
// while the producing one is still reading it - a client that disconnects
// mid-stream, or a gRPC send that fails. The decoder wrapped around the body
// must not go back to the pool while that read is in flight; if it does, the
// teardown deadlocks on the decoder it is trying to drain.
func TestUpstreamStreamTornDownWhileStillBeingRead(t *testing.T) {
	for _, scheme := range []compression.Scheme{compression.Zstd, compression.Gzip} {
		t.Run(string(scheme), func(te *testing.T) {
			plain := bytes.Repeat([]byte(`{"chunk":"0123456789abcdef"},`), 2048)
			encoded := encodeUpstream(te, string(scheme), plain)

			// Send enough of the body for a read to be under way, then hold the
			// response open so the read is still in flight when the test tears
			// the stream down.
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", string(scheme))
				w.WriteHeader(http.StatusOK)
				flusher, _ := w.(http.Flusher)
				if _, err := w.Write(encoded[:len(encoded)/2]); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
				<-release
			}))
			defer srv.Close()
			defer close(release)

			connector := restConnectorFor(te, &config.ApiConnectorConfig{Url: srv.URL})

			for range 4 {
				r := connector.SendRequest(
					context.Background(),
					protocol.NewStreamUpstreamRestRequest("1", "GET#/status", nil, nil, ""),
				)
				require.False(te, r.HasError())
				require.True(te, r.HasStream())

				reader := r.EncodeResponse([]byte("1"))
				closer, ok := reader.(io.Closer)
				require.True(te, ok)

				// One goroutine reads while this one closes, which is the
				// shape streamReadAhead creates on an early return.
				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _ = io.Copy(io.Discard, reader)
				}()
				time.Sleep(2 * time.Millisecond)

				closed := make(chan struct{})
				go func() {
					defer close(closed)
					_ = closer.Close()
				}()
				select {
				case <-closed:
				case <-time.After(15 * time.Second):
					te.Fatal("closing a stream that is still being read did not return")
				}
				wg.Wait()
			}

			// The pool has to be healthy afterwards: nothing in it may still
			// belong to one of the streams that were torn down.
			done := make(chan struct{})
			srvWhole := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", string(scheme))
				_, _ = w.Write(encoded)
			}))
			defer srvWhole.Close()
			go func() {
				defer close(done)
				whole := restConnectorFor(te, &config.ApiConnectorConfig{Url: srvWhole.URL}).
					SendRequest(context.Background(), protocol.NewUpstreamRestRequest("1", "GET#/status", nil, nil, ""))
				assert.False(te, whole.HasError())
				assert.Equal(te, plain, whole.ResponseResult())
			}()
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				te.Fatal("a later request could not get a working decoder from the pool")
			}
		})
	}
}
