package ws_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	wsupstream "github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewBaseWsProcessorReturnsErrorWhenDialServiceFails(t *testing.T) {
	dialService := mocks.NewDialWsServiceMock()
	registry := mocks.NewRequestRegistryMock()
	session := mocks.NewWsSessionMock()
	wsProtocol := mocks.NewWsProtocolMock()
	expectedErr := errors.New("dial init failed")

	dialService.On("NewConnectFunc", mock.Anything).Return(nil, expectedErr).Once()

	processor, err := wsupstream.NewBaseWsProcessor(context.Background(), "upstream-1", "ws://endpoint", dialService, registry, session, wsProtocol)

	assert.Nil(t, processor)
	require.ErrorIs(t, err, expectedErr)
	dialService.AssertExpectations(t)
}

func TestBaseWsProcessorSendWsRequestReturnsProtocolError(t *testing.T) {
	loadMethodSpecs(t)

	processor := newProcessorForNoStart(t)
	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	expectedErr := errors.New("frame error")
	processor.wsProtocol.On("RequestFrame", request).Return(nil, expectedErr).Once()

	responseChan, callErr := processor.processor.SendWsRequest(context.Background(), request)

	assert.Nil(t, responseChan)
	require.ErrorIs(t, callErr, expectedErr)
	processor.wsProtocol.AssertExpectations(t)
}

func TestBaseWsProcessorSendWsRequestReturnsErrorWhenSpecMethodMissing(t *testing.T) {
	processor := newProcessorForNoStart(t)
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("unknown_method", nil, chains.ETHEREUM)
	require.NoError(t, err)

	responseChan, callErr := processor.processor.SendWsRequest(context.Background(), request)

	assert.Nil(t, responseChan)
	require.EqualError(t, callErr, "no spec method found for unknown_method")
}

func TestBaseWsProcessorSendWsRequestReturnsErrorWhenMethodIsNotSubscription(t *testing.T) {
	loadMethodSpecs(t)

	processor := newProcessorForNoStart(t)
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	responseChan, callErr := processor.processor.SendWsRequest(context.Background(), request)

	assert.Nil(t, responseChan)
	require.EqualError(t, callErr, "'eth_blockNumber' is not subscribe method and it can't be sent via SendWsRequest, use SendRpcRequest instead")
}

func TestBaseWsProcessorSendWsRequest(t *testing.T) {
	loadMethodSpecs(t)

	processor := newStartedProcessor(t)
	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	reqOp := mocks.NewRequestOperationMock()
	frame := &wsupstream.RequestFrame{
		RequestId: "request-1",
		SubType:   "newHeads",
		Body:      []byte(`{"id":"request-1"}`),
	}

	processor.wsProtocol.On("RequestFrame", request).Return(frame, nil).Once()
	processor.wsProtocol.On("DoOnCloseFunc", mock.Anything).Return(wsupstream.DoOnClose(func(op wsupstream.RequestOperation) {})).Once()
	processor.requestRegistry.On("Register", mock.Anything, request, "request-1", "newHeads").Return(reqOp).Once()
	processor.requestRegistry.On("Start", reqOp, mock.Anything).Once()
	processor.wsSession.On("WriteMessage", "ws://endpoint", frame.Body).Return(nil).Once()

	responseChan, callErr := processor.processor.SendWsRequest(context.Background(), request)

	require.NoError(t, callErr)
	assert.Equal(t, reqOp.GetResponseChannel(), responseChan)
}

func TestBaseWsProcessorSendWsRequestAbortsWhenWriteFails(t *testing.T) {
	loadMethodSpecs(t)

	processor := newStartedProcessor(t)
	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	reqOp := mocks.NewRequestOperationMock()
	frame := &wsupstream.RequestFrame{
		RequestId: "request-1",
		SubType:   "newHeads",
		Body:      []byte(`{"id":"request-1"}`),
	}
	expectedErr := errors.New("write failed")

	processor.wsProtocol.On("RequestFrame", request).Return(frame, nil).Once()
	processor.wsProtocol.On("DoOnCloseFunc", mock.Anything).Return(wsupstream.DoOnClose(func(op wsupstream.RequestOperation) {})).Once()
	processor.requestRegistry.On("Register", mock.Anything, request, "request-1", "newHeads").Return(reqOp).Once()
	processor.requestRegistry.On("Start", reqOp, mock.Anything).Once()
	processor.wsSession.On("WriteMessage", "ws://endpoint", frame.Body).Return(expectedErr).Once()
	processor.requestRegistry.On("Abort", "request-1", reqOp).Once()

	responseChan, callErr := processor.processor.SendWsRequest(context.Background(), request)

	assert.Nil(t, responseChan)
	require.ErrorIs(t, callErr, expectedErr)
}

func TestBaseWsProcessorSendRpcRequest(t *testing.T) {
	processor := newStartedProcessor(t)
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	reqOp := mocks.NewRequestOperationMock()
	expectedResponse := &protocol.WsResponse{Id: "request-1", Type: protocol.JsonRpc, Message: []byte(`"0x1"`)}
	reqOp.ResponseChannel <- expectedResponse

	frame := &wsupstream.RequestFrame{
		RequestId: "request-1",
		Body:      []byte(`{"id":"request-1"}`),
	}

	processor.wsProtocol.On("RequestFrame", request).Return(frame, nil).Once()
	processor.wsProtocol.On("DoOnCloseFunc", mock.Anything).Return(wsupstream.DoOnClose(func(op wsupstream.RequestOperation) {})).Once()
	processor.requestRegistry.On("Register", mock.Anything, request, "request-1", "").Return(reqOp).Once()
	processor.requestRegistry.On("Start", reqOp, mock.Anything).Once()
	processor.wsSession.On("WriteMessage", "ws://endpoint", frame.Body).Return(nil).Once()

	response, callErr := processor.processor.SendRpcRequest(context.Background(), request)

	require.NoError(t, callErr)
	assert.Same(t, expectedResponse, response)
}

func TestBaseWsProcessorSendRpcRequestReturnsErrorWhenChannelClosed(t *testing.T) {
	processor := newStartedProcessor(t)
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	reqOp := mocks.NewRequestOperationMock()
	close(reqOp.ResponseChannel)

	frame := &wsupstream.RequestFrame{
		RequestId: "request-1",
		Body:      []byte(`{"id":"request-1"}`),
	}

	processor.wsProtocol.On("RequestFrame", request).Return(frame, nil).Once()
	processor.wsProtocol.On("DoOnCloseFunc", mock.Anything).Return(wsupstream.DoOnClose(func(op wsupstream.RequestOperation) {})).Once()
	processor.requestRegistry.On("Register", mock.Anything, request, "request-1", "").Return(reqOp).Once()
	processor.requestRegistry.On("Start", reqOp, mock.Anything).Once()
	processor.wsSession.On("WriteMessage", "ws://endpoint", frame.Body).Return(nil).Once()

	response, callErr := processor.processor.SendRpcRequest(context.Background(), request)

	assert.Nil(t, response)
	require.EqualError(t, callErr, "no response on method eth_blockNumber via ws")
}

func TestBaseWsProcessorSendRpcRequestReturnsContextError(t *testing.T) {
	processor := newStartedProcessor(t)
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	reqOp := mocks.NewRequestOperationMock()
	frame := &wsupstream.RequestFrame{
		RequestId: "request-1",
		Body:      []byte(`{"id":"request-1"}`),
	}

	processor.wsProtocol.On("RequestFrame", request).Return(frame, nil).Once()
	processor.wsProtocol.On("DoOnCloseFunc", mock.Anything).Return(wsupstream.DoOnClose(func(op wsupstream.RequestOperation) {})).Once()
	processor.requestRegistry.On("Register", mock.Anything, request, "request-1", "").Return(reqOp).Once()
	processor.requestRegistry.On("Start", reqOp, mock.Anything).Once()
	processor.wsSession.On("WriteMessage", "ws://endpoint", frame.Body).Return(nil).Once()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	response, callErr := processor.processor.SendRpcRequest(ctx, request)

	assert.Nil(t, response)
	require.EqualError(t, callErr, "no response on method eth_blockNumber via ws due to context canceled")
}

func TestBaseWsProcessorStartPublishesConnectedAndRoutesRpcMessages(t *testing.T) {
	serverConnReady := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		serverConnReady <- conn
	}))
	defer server.Close()

	dialService := mocks.NewDialWsServiceMock()
	requestRegistry := mocks.NewRequestRegistryMock()
	wsProtocol := mocks.NewWsProtocolMock()
	session := wsupstream.NewWebsocketSession()

	dialFunc := func() (*websocket.Conn, error) {
		conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http://", "ws://", 1), nil)
		if err != nil {
			return nil, err
		}
		return conn, nil
	}
	dialService.On("NewConnectFunc", mock.Anything).Return(wsupstream.DialFunc(dialFunc), nil).Once()

	expectedResponse := &protocol.WsResponse{Id: "request-1", Type: protocol.JsonRpc, Message: []byte(`"0x1"`)}
	wsProtocol.On("ParseWsMessage", []byte(`message-1`)).Return(expectedResponse, nil).Once()
	routed := make(chan struct{}, 1)
	requestRegistry.On("OnRpcMessage", expectedResponse).Run(func(args mock.Arguments) {
		routed <- struct{}{}
	}).Once()
	requestRegistry.On("CancelAll").Maybe()

	processor, err := wsupstream.NewBaseWsProcessor(context.Background(), "upstream-1", "ws://endpoint", dialService, requestRegistry, session, wsProtocol)
	require.NoError(t, err)

	subscription := processor.SubscribeWsStates("test")
	processor.Start()
	defer processor.Stop()

	select {
	case state := <-subscription.Events:
		assert.Equal(t, protocol.WsConnected, state)
	case <-time.After(time.Second):
		t.Fatal("expected connected state")
	}

	serverConn := <-serverConnReady
	defer func() {
		_ = serverConn.Close()
	}()
	require.NoError(t, serverConn.WriteMessage(websocket.TextMessage, []byte(`message-1`)))

	select {
	case <-routed:
	case <-time.After(time.Second):
		t.Fatal("expected rpc message to be routed")
	}
}

func TestBaseWsProcessorStartPublishesDisconnectedOnReadError(t *testing.T) {
	serverConnReady := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		serverConnReady <- conn
	}))
	defer server.Close()

	dialService := mocks.NewDialWsServiceMock()
	requestRegistry := mocks.NewRequestRegistryMock()
	wsProtocol := mocks.NewWsProtocolMock()
	session := wsupstream.NewWebsocketSession()

	dialFunc := func() (*websocket.Conn, error) {
		conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http://", "ws://", 1), nil)
		if err != nil {
			return nil, err
		}
		return conn, nil
	}
	dialService.On("NewConnectFunc", mock.Anything).Return(wsupstream.DialFunc(dialFunc), nil).Once()
	requestRegistry.On("CancelAll").Maybe()

	processor, err := wsupstream.NewBaseWsProcessor(context.Background(), "upstream-1", "ws://endpoint", dialService, requestRegistry, session, wsProtocol)
	require.NoError(t, err)

	subscription := processor.SubscribeWsStates("test")
	processor.Start()
	defer processor.Stop()

	select {
	case state := <-subscription.Events:
		assert.Equal(t, protocol.WsConnected, state)
	case <-time.After(time.Second):
		t.Fatal("expected connected state")
	}

	serverConn := <-serverConnReady
	require.NoError(t, serverConn.Close())

	select {
	case state := <-subscription.Events:
		assert.Equal(t, protocol.WsDisconnected, state)
	case <-time.After(time.Second):
		t.Fatal("expected disconnected state")
	}
}

type processorFixture struct {
	processor       *wsupstream.BaseWsProcessor
	dialService     *mocks.DialWsServiceMock
	requestRegistry *mocks.RequestRegistryMock
	wsSession       *mocks.WsSessionMock
	wsProtocol      *mocks.WsProtocolMock
}

func newProcessorForNoStart(t *testing.T) *processorFixture {
	t.Helper()

	dialService := mocks.NewDialWsServiceMock()
	requestRegistry := mocks.NewRequestRegistryMock()
	wsSession := mocks.NewWsSessionMock()
	wsProtocol := mocks.NewWsProtocolMock()

	dialService.On("NewConnectFunc", mock.Anything).Return(wsupstream.DialFunc(func() (*websocket.Conn, error) {
		return nil, nil
	}), nil).Once()

	processor, err := wsupstream.NewBaseWsProcessor(context.Background(), "upstream-1", "ws://endpoint", dialService, requestRegistry, wsSession, wsProtocol)
	require.NoError(t, err)

	return &processorFixture{
		processor:       processor,
		dialService:     dialService,
		requestRegistry: requestRegistry,
		wsSession:       wsSession,
		wsProtocol:      wsProtocol,
	}
}

func newStartedProcessor(t *testing.T) *processorFixture {
	t.Helper()

	fixture := newProcessorForNoStart(t)
	fixture.wsSession.On("IsClosed").Return(false)
	fixture.wsSession.On("CloseCurrent").Return(nil).Maybe()
	fixture.requestRegistry.On("CancelAll").Maybe()

	fixture.processor.Start()
	t.Cleanup(func() {
		fixture.processor.Stop()
		require.Eventually(t, func() bool {
			return fixture.requestRegistry.AssertExpectations(t) &&
				fixture.wsSession.AssertExpectations(t) &&
				fixture.wsProtocol.AssertExpectations(t) &&
				fixture.dialService.AssertExpectations(t)
		}, time.Second, 10*time.Millisecond)
	})

	return fixture
}
