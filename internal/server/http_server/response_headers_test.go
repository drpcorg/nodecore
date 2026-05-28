package http_server

import (
	"net/http"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REST clients depend on the upstream's Content-Type / signature / CORS
// headers reaching them verbatim. This test pins that path so a future
// refactor can't silently drop them.
func TestCopyUpstreamResponseHeaders_ForwardsBaseUpstreamHeaders(t *testing.T) {
	upstream := http.Header{
		"Content-Type":    {"application/json"},
		"X-Custom-Header": {"alpha", "beta"},
	}
	resp := protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"ok":true}`), protocol.Rest).
		WithResponseHeaders(upstream)

	out := http.Header{}
	copyUpstreamResponseHeaders(out, resp)

	assert.Equal(t, []string{"application/json"}, out["Content-Type"])
	assert.Equal(t, []string{"alpha", "beta"}, out["X-Custom-Header"],
		"repeated header values must round-trip onto the client response")
}

// Responses that don't carry headers (e.g. error replies) must be a no-op,
// not a panic - the copy helper has to tolerate every ResponseHolder shape
// the channel might emit.
func TestCopyUpstreamResponseHeaders_NoHeadersIsNoOp(t *testing.T) {
	resp := protocol.NewTotalFailureFromErr("1", protocol.ServerError(), protocol.Rest)

	out := http.Header{}
	require.NotPanics(t, func() {
		copyUpstreamResponseHeaders(out, resp)
	})
	assert.Empty(t, out)
}

// nil ResponseHeaders shouldn't blow up either - WithResponseHeaders was
// never called.
func TestCopyUpstreamResponseHeaders_NilHeadersIsNoOp(t *testing.T) {
	resp := protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{}`), protocol.Rest)

	out := http.Header{}
	require.NotPanics(t, func() {
		copyUpstreamResponseHeaders(out, resp)
	})
	assert.Empty(t, out)
}
