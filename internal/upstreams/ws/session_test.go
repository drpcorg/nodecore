package ws_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	wsupstream "github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWsSession(t *testing.T) {
	session := wsupstream.NewWebsocketSession()

	assert.True(t, session.IsClosed())
	assert.Nil(t, session.LoadConnection())
}

func TestWsSessionSetConnection(t *testing.T) {
	session := wsupstream.NewWebsocketSession()
	clientConn, serverMessages, serverErrors, closeClient := newTestWebsocketConnection(t)
	defer closeClient()

	session.SetConnection(clientConn)

	assert.False(t, session.IsClosed())
	assert.Same(t, clientConn, session.LoadConnection())
	assertNoServerActivity(t, serverMessages, serverErrors)
}

func TestWsSessionWriteMessageReturnsErrorWhenClosed(t *testing.T) {
	session := wsupstream.NewWebsocketSession()

	err := session.WriteMessage("ws://example", []byte(`hello`))

	require.EqualError(t, err, "no connection to ws://example")
}

func TestWsSessionWriteMessage(t *testing.T) {
	session := wsupstream.NewWebsocketSession()
	clientConn, serverMessages, _, closeClient := newTestWebsocketConnection(t)
	defer closeClient()

	session.SetConnection(clientConn)

	err := session.WriteMessage("ws://example", []byte(`hello`))
	require.NoError(t, err)

	select {
	case message := <-serverMessages:
		assert.Equal(t, []byte(`hello`), message)
	case <-time.After(time.Second):
		t.Fatal("expected websocket message")
	}
}

func TestWsSessionCloseCurrentWithoutConnection(t *testing.T) {
	session := wsupstream.NewWebsocketSession()

	err := session.CloseCurrent()

	require.NoError(t, err)
	assert.True(t, session.IsClosed())
}

func TestWsSessionCloseCurrent(t *testing.T) {
	session := wsupstream.NewWebsocketSession()
	clientConn, _, serverErrors, closeClient := newTestWebsocketConnection(t)
	defer closeClient()

	session.SetConnection(clientConn)

	err := session.CloseCurrent()
	require.NoError(t, err)
	assert.True(t, session.IsClosed())

	select {
	case err := <-serverErrors:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("expected websocket close on server side")
	}

	writeErr := session.WriteMessage("ws://example", []byte(`hello`))
	require.EqualError(t, writeErr, "no connection to ws://example")
}

func newTestWebsocketConnection(
	t *testing.T,
) (
	*websocket.Conn,
	<-chan []byte,
	<-chan error,
	func(),
) {
	t.Helper()

	messageChan := make(chan []byte, 1)
	errorChan := make(chan error, 1)
	upgradeErrChan := make(chan error, 1)
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			upgradeErrChan <- err
			return
		}

		go func() {
			defer func() {
				_ = conn.Close()
			}()

			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					errorChan <- err
					return
				}
				messageChan <- message
			}
		}()
	}))

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	select {
	case err := <-upgradeErrChan:
		require.NoError(t, err)
	default:
	}

	closeFunc := func() {
		_ = clientConn.Close()
		server.Close()
	}

	return clientConn, messageChan, errorChan, closeFunc
}

func assertNoServerActivity(t *testing.T, messages <-chan []byte, errors <-chan error) {
	t.Helper()

	select {
	case message := <-messages:
		t.Fatalf("did not expect server message: %s", string(message))
	case err := <-errors:
		t.Fatalf("did not expect server error: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}
