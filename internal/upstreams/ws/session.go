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
}

type WebsocketSession struct {
	connection *utils.Atomic[*websocket.Conn]
	connClosed *atomic.Bool
}

func NewWebsocketSession() *WebsocketSession {
	connClosed := atomic.Bool{}
	connClosed.Store(true)

	return &WebsocketSession{
		connection: utils.NewAtomic[*websocket.Conn](),
		connClosed: &connClosed,
	}
}

func (s *WebsocketSession) SetConnection(conn *websocket.Conn) {
	s.connection.Store(conn)
	s.connClosed.Store(false)
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
