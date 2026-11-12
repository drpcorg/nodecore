package http_server_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/server/http_server"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type appCtxKey struct{}

// freeAddr returns a loopback address nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}

// writeSelfSignedCert puts a certificate for 127.0.0.1 and its key into a
// temporary directory and returns their absolute paths, which is how
// `server.tls` is normally filled in.
func writeSelfSignedCert(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDer, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}), 0o600))
	return certPath, keyPath
}

// echoingAppCtx answers with the value the request context inherited from the
// server's base context.
func echoingAppCtx() *echo.Echo {
	e := echo.New()
	e.GET("/", func(c *echo.Context) error {
		return c.String(http.StatusOK, fmt.Sprint(c.Request().Context().Value(appCtxKey{})))
	})
	return e
}

func getBody(client *http.Client, url string) (string, *http.Response, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return string(body), resp, err
}

// Start has to block until Shutdown and then report http.ErrServerClosed: the
// app tells a server that could not start from one that was asked to stop by
// exactly that.
func TestEchoServerServesUntilShutdown(t *testing.T) {
	ctx := context.WithValue(context.Background(), appCtxKey{}, "from the app context")
	server := http_server.NewEchoServer(echoingAppCtx(), http_server.WithClientLimits(ctx))

	addr := freeAddr(t)
	stopped := make(chan error, 1)
	go func() { stopped <- server.Start(addr, nil) }()

	var body string
	require.Eventually(t, func() bool {
		var err error
		body, _, err = getBody(http.DefaultClient, "http://"+addr+"/")
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)
	assert.Equal(t, "from the app context", body, "request contexts must descend from the app context")

	require.NoError(t, server.Shutdown(context.Background()))
	assert.ErrorIs(t, <-stopped, http.ErrServerClosed)

	_, _, err := getBody(http.DefaultClient, "http://"+addr+"/")
	assert.Error(t, err, "a server that was shut down must not accept connections")
}

// The certificate and the key are configured as absolute paths. Both HTTP
// versions have to be on offer: a WebSocket handshake needs http/1.1.
func TestEchoServerServesTls(t *testing.T) {
	certPath, keyPath := writeSelfSignedCert(t)
	server := http_server.NewEchoServer(echoingAppCtx())

	addr := freeAddr(t)
	stopped := make(chan error, 1)
	go func() {
		stopped <- server.Start(addr, &config.TlsConfig{Enabled: true, Certificate: certPath, Key: keyPath})
	}()
	defer func() {
		require.NoError(t, server.Shutdown(context.Background()))
		assert.ErrorIs(t, <-stopped, http.ErrServerClosed)
	}()

	for _, proto := range []string{"http/1.1", "h2"} {
		t.Run(proto, func(te *testing.T) {
			client := &http.Client{Transport: &http.Transport{
				ForceAttemptHTTP2: proto == "h2",
				TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, NextProtos: []string{proto}},
			}}
			var resp *http.Response
			require.Eventually(te, func() bool {
				var err error
				_, resp, err = getBody(client, "https://"+addr+"/")
				return err == nil
			}, 5*time.Second, 20*time.Millisecond)
			assert.Equal(te, http.StatusOK, resp.StatusCode)
			require.NotNil(te, resp.TLS)
			assert.Equal(te, proto, resp.TLS.NegotiatedProtocol)
		})
	}
}

func TestEchoServerReportsAFailedStart(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	err = http_server.NewEchoServer(echo.New()).Start(listener.Addr().String(), nil)

	require.Error(t, err)
	assert.NotErrorIs(t, err, http.ErrServerClosed)
}
