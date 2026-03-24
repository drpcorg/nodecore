package ws_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	wsupstream "github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestRegistryRegister(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "newHeads")

	require.NotNil(t, req.GetResponseChannel())
	require.NotNil(t, req.GetInternalChannel())
	assert.Equal(t, "eth_subscribe", req.Method())
	assert.Equal(t, "newHeads", req.SubType())
	assert.False(t, req.IsCompleted())
	assert.True(t, req.ShouldDoOnClose())
	assert.Empty(t, req.SubID())
}

func TestRequestRegistryStartForwardsInternalMessages(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "")
	registry.Start(req, func(op wsupstream.RequestOperation) {})

	message := &protocol.WsResponse{Id: "request-1", Type: protocol.JsonRpc, Message: []byte(`"0x1"`)}
	req.WriteInternal(message)

	assertSameMessage(t, req.GetResponseChannel(), message)
}

func TestRequestRegistryStartCallsDoOnCloseOnCancel(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "")
	done := make(chan wsupstream.RequestOperation, 1)
	registry.Start(req, func(op wsupstream.RequestOperation) {
		done <- op
	})

	req.Cancel()

	select {
	case got := <-done:
		assert.Same(t, req, got)
	case <-time.After(time.Second):
		t.Fatal("expected doOnClose to be called")
	}

	require.Eventually(t, req.IsCompleted, time.Second, 10*time.Millisecond)
	assertClosedChannel(t, req.GetResponseChannel())
}

func TestRequestRegistryStartSkipsDoOnCloseWhenDisabled(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "")
	req.SetSkipDoOnClose()

	called := make(chan struct{}, 1)
	registry.Start(req, func(op wsupstream.RequestOperation) {
		called <- struct{}{}
	})

	req.Cancel()

	select {
	case <-called:
		t.Fatal("did not expect doOnClose to be called")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRequestRegistryAbortCancelsOperationAndRemovesRequest(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "")
	registry.Start(req, func(op wsupstream.RequestOperation) {})

	registry.Abort("request-1", req)

	assert.False(t, req.ShouldDoOnClose())
	assertDone(t, req.Done())

	registry.OnRpcMessage(&protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0x1"`),
	})

	assertNoMessage(t, req.GetResponseChannel())
}

func TestRequestRegistryOnRpcMessageRoutesUnaryResponseOnce(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "")
	registry.Start(req, func(op wsupstream.RequestOperation) {})

	first := &protocol.WsResponse{Id: "request-1", Type: protocol.JsonRpc, Message: []byte(`"0x1"`)}
	registry.OnRpcMessage(first)
	assertSameMessage(t, req.GetResponseChannel(), first)

	registry.OnRpcMessage(&protocol.WsResponse{Id: "request-1", Type: protocol.JsonRpc, Message: []byte(`"0x2"`)})
	assertNoMessage(t, req.GetResponseChannel())
}

func TestRequestRegistryOnRpcMessageStoresSubscriptionAndRoutesEvents(t *testing.T) {
	loadMethodSpecs(t)

	registry := wsupstream.NewBaseRequestRegistry(chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "newHeads")
	registry.Start(req, func(op wsupstream.RequestOperation) {})

	subscriptionResponse := &protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0xsub"`),
	}
	registry.OnRpcMessage(subscriptionResponse)
	assertSameMessage(t, req.GetResponseChannel(), subscriptionResponse)
	assert.Equal(t, "0xsub", req.SubID())

	event := &protocol.WsResponse{
		Type:    protocol.Ws,
		SubId:   "0xsub",
		Message: []byte(`{"number":"0x1"}`),
	}
	registry.OnSubscriptionMessage(event)
	assertSameMessage(t, req.GetResponseChannel(), event)
}

func TestRequestRegistryOnRpcMessageWithErrorCancelsOperation(t *testing.T) {
	loadMethodSpecs(t)

	registry := wsupstream.NewBaseRequestRegistry(chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "newHeads")
	registry.Start(req, func(op wsupstream.RequestOperation) {})

	response := &protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`{"message":"error","code":500}`),
		Error:   protocol.ServerError(),
	}
	registry.OnRpcMessage(response)

	assertSameMessage(t, req.GetResponseChannel(), response)
	assertDone(t, req.Done())
}

func TestRequestRegistryCancelAllCancelsRequestsAndSubscriptionsWithoutDoOnClose(t *testing.T) {
	loadMethodSpecs(t)

	registry := wsupstream.NewBaseRequestRegistry(chains.ETHEREUM, "upstream-1", "eth")

	unaryRequest, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)
	unaryOp := registry.Register(context.Background(), unaryRequest, "request-1", "")

	subRequest, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)
	subOp := registry.Register(context.Background(), subRequest, "request-2", "newHeads")

	called := make(chan struct{}, 2)
	registry.Start(unaryOp, func(op wsupstream.RequestOperation) {
		called <- struct{}{}
	})
	registry.Start(subOp, func(op wsupstream.RequestOperation) {
		called <- struct{}{}
	})

	subscriptionResponse := &protocol.WsResponse{
		Id:      "request-2",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0xsub"`),
	}
	registry.OnRpcMessage(subscriptionResponse)
	assertSameMessage(t, subOp.GetResponseChannel(), subscriptionResponse)

	registry.CancelAll()

	assert.False(t, unaryOp.ShouldDoOnClose())
	assert.False(t, subOp.ShouldDoOnClose())
	assertDone(t, unaryOp.Done())
	assertDone(t, subOp.Done())

	select {
	case <-called:
		t.Fatal("did not expect doOnClose after CancelAll")
	case <-time.After(200 * time.Millisecond):
	}

	registry.OnRpcMessage(&protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0x1"`),
	})
	registry.OnSubscriptionMessage(&protocol.WsResponse{
		Type:    protocol.Ws,
		SubId:   "0xsub",
		Message: []byte(`{"number":"0x1"}`),
	})

	assertNoMessage(t, unaryOp.GetResponseChannel())
	assertNoMessage(t, subOp.GetResponseChannel())
}

func assertSameMessage(t *testing.T, ch <-chan *protocol.WsResponse, expected *protocol.WsResponse) {
	t.Helper()

	select {
	case got := <-ch:
		assert.Same(t, expected, got)
	case <-time.After(time.Second):
		t.Fatal("expected message")
	}
}

func assertNoMessage(t *testing.T, ch <-chan *protocol.WsResponse) {
	t.Helper()

	select {
	case got, ok := <-ch:
		if ok {
			t.Fatalf("did not expect message: %#v", got)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func assertDone(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected done channel to close")
	}
}

func assertClosedChannel(t *testing.T, ch <-chan *protocol.WsResponse) {
	t.Helper()

	select {
	case _, ok := <-ch:
		assert.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("expected channel to close")
	}
}
