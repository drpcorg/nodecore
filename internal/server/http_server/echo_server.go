package http_server

import (
	"context"
	"net/http"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/labstack/echo/v5"
)

// EchoServer serves an echo instance: echo is only an http.Handler, the
// http.Server around it belongs to whoever runs it.
//
// echo.StartConfig is deliberately not used for that. It shuts the server down
// on its own when a context ends, which takes away the order nodecore stops
// its servers in; it reads the certificate through an fs.FS, which rejects the
// absolute paths `server.tls` is normally given; and it puts a 30s read
// timeout on every server it starts.
type EchoServer struct {
	server *http.Server
}

// NewEchoServer wraps e in a server with net/http's defaults. Options adjust
// that server before it starts.
func NewEchoServer(e *echo.Echo, options ...func(*http.Server)) *EchoServer {
	server := &http.Server{Handler: e}
	for _, option := range options {
		option(server)
	}
	return &EchoServer{server: server}
}

// Start listens on addr and serves until Shutdown, which makes it return
// http.ErrServerClosed. With TLS enabled the connection is offered both h2 and
// http/1.1, the latter being what a WebSocket handshake needs.
func (s *EchoServer) Start(addr string, tlsCfg *config.TlsConfig) error {
	s.server.Addr = addr
	if tlsCfg != nil && tlsCfg.Enabled {
		return s.server.ListenAndServeTLS(tlsCfg.Certificate, tlsCfg.Key)
	}
	return s.server.ListenAndServe()
}

// Shutdown stops accepting connections and waits for the requests in flight
// until ctx ends.
func (s *EchoServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}
