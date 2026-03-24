package ws_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	wsupstream "github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultDialWsServiceNewConnectFunc(t *testing.T) {
	headerChan := make(chan http.Header, 1)
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerChan <- r.Header.Clone()

		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer func() {
			_ = conn.Close()
		}()

		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`ok`)))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	dialService := wsupstream.NewDefaultDialWsService(&config.ApiConnectorConfig{
		Url: strings.Replace(server.URL, "http://", "ws://", 1),
		Headers: map[string]string{
			"X-Test-Header": "header-value",
		},
	}, "")

	connectFunc, err := dialService.NewConnectFunc(ctx)
	require.NoError(t, err)

	conn, err := connectFunc()
	require.NoError(t, err)
	defer func() {
		_ = conn.Close()
	}()

	select {
	case header := <-headerChan:
		assert.Equal(t, "header-value", header.Get("X-Test-Header"))
	case <-time.After(time.Second):
		t.Fatal("expected websocket request headers")
	}
}

func TestDefaultDialWsServiceNewConnectFuncReturnsErrorForOnionEndpointWithoutTorProxy(t *testing.T) {
	dialService := wsupstream.NewDefaultDialWsService(&config.ApiConnectorConfig{
		Url: "ws://exampleonionaddress.onion",
	}, "")

	connectFunc, err := dialService.NewConnectFunc(context.Background())

	assert.Nil(t, connectFunc)
	require.EqualError(t, err, "tor proxy url is required for onion endpoints")
}

func TestDefaultDialWsServiceNewConnectFuncReturnsErrorForInvalidCAPath(t *testing.T) {
	dialService := wsupstream.NewDefaultDialWsService(&config.ApiConnectorConfig{
		Url: "ws://localhost",
		Ca:  "/path/that/does/not/exist.pem",
	}, "")

	connectFunc, err := dialService.NewConnectFunc(context.Background())

	assert.Nil(t, connectFunc)
	require.Error(t, err)
}
