package utils_test

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultHttpTransportHasHttp2HealthCheck(t *testing.T) {
	transport := utils.DefaultHttpTransport()

	require.NotNil(t, transport.HTTP2)
	assert.Positive(t, transport.HTTP2.SendPingTimeout)
	assert.Positive(t, transport.HTTP2.PingTimeout)
}

func TestDefaultHttpTransportReplacesSilentHttp2Connection(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	listener := &freezableListener{Listener: inner}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	server.Listener = listener
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	transport := utils.DefaultHttpTransport()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	// only shorten the configured health check, never add one
	if transport.HTTP2 != nil && transport.HTTP2.SendPingTimeout > 0 {
		transport.HTTP2.SendPingTimeout = 300 * time.Millisecond
		transport.HTTP2.PingTimeout = 300 * time.Millisecond
	}
	client := &http.Client{Transport: transport}

	get := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
		return err
	}

	require.NoError(t, get())
	require.Equal(t, int32(1), listener.accepted.Load())

	listener.freezeExisting()
	time.Sleep(2 * time.Second)

	start := time.Now()
	require.NoError(t, get())
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Equal(t, int32(2), listener.accepted.Load())
}

// freezableListener simulates a peer that vanished without closing the connection
type freezableListener struct {
	net.Listener
	mu       sync.Mutex
	conns    []*freezableConn
	accepted atomic.Int32
}

func (l *freezableListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.accepted.Add(1)
	fc := &freezableConn{Conn: conn}
	l.mu.Lock()
	l.conns = append(l.conns, fc)
	l.mu.Unlock()
	return fc, nil
}

func (l *freezableListener) freezeExisting() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		c.frozen.Store(true)
	}
}

type freezableConn struct {
	net.Conn
	frozen atomic.Bool
}

func (c *freezableConn) Read(b []byte) (int, error) {
	for {
		n, err := c.Conn.Read(b)
		if err != nil || !c.frozen.Load() {
			return n, err
		}
	}
}

func (c *freezableConn) Write(b []byte) (int, error) {
	if c.frozen.Load() {
		return len(b), nil
	}
	return c.Conn.Write(b)
}
