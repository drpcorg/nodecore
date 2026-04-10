package mocks

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
)

type DialWsServiceMock struct {
	mock.Mock
}

func NewDialWsServiceMock() *DialWsServiceMock {
	return &DialWsServiceMock{}
}

func (m *DialWsServiceMock) NewConnectFunc(ctx context.Context) (ws.DialFunc, error) {
	args := m.Called(ctx)
	var dialFunc ws.DialFunc
	if args.Get(0) != nil {
		dialFunc = args.Get(0).(ws.DialFunc)
	}
	return dialFunc, args.Error(1)
}

type RequestRegistryMock struct {
	mock.Mock
}

func NewRequestRegistryMock() *RequestRegistryMock {
	return &RequestRegistryMock{}
}

func (m *RequestRegistryMock) Start(req ws.RequestOperation) {
	m.Called(req)
}

func (m *RequestRegistryMock) Abort(requestId string) {
	m.Called(requestId)
}

func (m *RequestRegistryMock) Register(
	ctx context.Context,
	request protocol.RequestHolder,
	requestId, subType string,
	doOnClose ws.DoOnClose,
) ws.RequestOperation {
	args := m.Called(ctx, request, requestId, subType, doOnClose)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(ws.RequestOperation)
}

func (m *RequestRegistryMock) Cancel(opId string) {
	m.Called(opId)
}

func (m *RequestRegistryMock) CancelAll() {
	m.Called()
}

func (m *RequestRegistryMock) OnRpcMessage(response *protocol.WsResponse) {
	m.Called(response)
}

func (m *RequestRegistryMock) OnSubscriptionMessage(response *protocol.WsResponse) {
	m.Called(response)
}

type WsSessionMock struct {
	mock.Mock
}

func NewWsSessionMock() *WsSessionMock {
	return &WsSessionMock{}
}

func (m *WsSessionMock) SetConnection(conn *websocket.Conn) {
	m.Called(conn)
}

func (m *WsSessionMock) WriteMessage(endpoint string, message []byte) error {
	args := m.Called(endpoint, message)
	return args.Error(0)
}

func (m *WsSessionMock) CloseCurrent() error {
	args := m.Called()
	return args.Error(0)
}

func (m *WsSessionMock) IsClosed() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *WsSessionMock) LoadConnection() *websocket.Conn {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*websocket.Conn)
}

type WsProtocolMock struct {
	mock.Mock
}

func NewWsProtocolMock() *WsProtocolMock {
	return &WsProtocolMock{}
}

func (m *WsProtocolMock) RequestFrame(request protocol.RequestHolder) (*ws.RequestFrame, error) {
	args := m.Called(request)
	var frame *ws.RequestFrame
	if args.Get(0) != nil {
		frame = args.Get(0).(*ws.RequestFrame)
	}
	return frame, args.Error(1)
}

func (m *WsProtocolMock) DoOnCloseFunc(writeRequestFunc ws.WriteRequest) ws.DoOnClose {
	args := m.Called(writeRequestFunc)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(ws.DoOnClose)
}

func (m *WsProtocolMock) ParseWsMessage(payload []byte) (*protocol.WsResponse, error) {
	args := m.Called(payload)
	var response *protocol.WsResponse
	if args.Get(0) != nil {
		response = args.Get(0).(*protocol.WsResponse)
	}
	return response, args.Error(1)
}
