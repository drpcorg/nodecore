package ws

import (
	"fmt"
	"sync/atomic"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/gorilla/websocket"
)

type WsSession interface {
	SetConnection(conn *websocket.Conn)
	WriteMessage(endpoint string, message []byte) error
	CloseCurrent() error

	IsClosed() bool
	LoadConnection() *websocket.Conn
	// Generation returns a monotonic counter bumped on every successful
	// SetConnection. It identifies the current connection so that disconnect
	// events emitted by a reader of a superseded connection can be ignored.
	Generation() uint64
}

type WebsocketSession struct {
	connection *utils.Atomic[*websocket.Conn]
	connClosed *atomic.Bool
	generation *atomic.Uint64
}

func NewWebsocketSession() *WebsocketSession {
	connClosed := atomic.Bool{}
	connClosed.Store(true)

	return &WebsocketSession{
		connection: utils.NewAtomic[*websocket.Conn](),
		connClosed: &connClosed,
		generation: &atomic.Uint64{},
	}
}

func (s *WebsocketSession) SetConnection(conn *websocket.Conn) {
	s.connection.Store(conn)
	s.connClosed.Store(false)
	s.generation.Add(1)
}

func (s *WebsocketSession) Generation() uint64 {
	return s.generation.Load()
}

func (s *WebsocketSession) IsClosed() bool {
	return s.connClosed.Load()
}

func (s *WebsocketSession) LoadConnection() *websocket.Conn {
	return s.connection.Load()
}

func (s *WebsocketSession) WriteMessage(endpoint string, message []byte) error {
	if s.IsClosed() {
		return fmt.Errorf("no connection to %s", endpoint)
	}

	return s.connection.Load().WriteMessage(websocket.TextMessage, message)
}

func (s *WebsocketSession) CloseCurrent() error {
	s.connClosed.Store(true)
	conn := s.connection.Load()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

var _ WsSession = (*WebsocketSession)(nil)
