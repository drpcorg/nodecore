package connectors_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowBodyServer answers immediately with headers and the first bytes of a body, then stalls
// before finishing it — the shape of a large REST response that takes longer than a client-side
// budget to stream.
func slowBodyServer(t *testing.T, stall time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":["`))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(stall)
		_, _ = w.Write([]byte(`slow"]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func readStreamedBody(t *testing.T, ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	t.Helper()
	connector, err := connectors.NewHttpConnector(&config.ApiConnectorConfig{Url: url}, specs.RestConnector, "", "test-upstream", timeout)
	require.NoError(t, err)

	req := protocol.NewStreamUpstreamRestRequest("1", "GET#/slow", nil, nil, "")
	r := connector.SendRequest(ctx, req)
	require.False(t, r.HasError(), "headers arrive immediately, so the request itself must not fail")
	require.True(t, r.HasStream())

	return io.ReadAll(r.EncodeResponse([]byte("1")))
}

// The configured http-response-timeout is the total budget for the exchange, body included: a body
// that streams for longer than the budget is cut, while 0 disables the client-side budget entirely
// and leaves termination to the caller's context.
func TestHttpConnectorHttpResponseTimeout(t *testing.T) {
	srv := slowBodyServer(t, 300*time.Millisecond)

	t.Run("a body slower than the timeout is cut with a timeout error", func(t *testing.T) {
		_, err := readStreamedBody(t, context.Background(), srv.URL, 100*time.Millisecond)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Client.Timeout")
	})

	t.Run("zero timeout streams the whole body however slow", func(t *testing.T) {
		body, err := readStreamedBody(t, context.Background(), srv.URL, 0)
		require.NoError(t, err)
		assert.Equal(t, `{"data":["slow"]}`, string(body))
	})

	t.Run("zero timeout still ends when the caller's context does", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err := readStreamedBody(t, ctx, srv.URL, 0)
		require.Error(t, err, "with no client-side budget the caller's context is the only terminator")
	})
}
