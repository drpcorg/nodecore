package ws_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	wsupstream "github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestRegistryRegister(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "newHeads", func(wsupstream.RequestOperation) {})

	require.NotNil(t, req.GetChannel(wsupstream.MessageResponse))
	require.NotNil(t, req.GetChannel(wsupstream.MessageInternal))
	assert.Equal(t, "eth_subscribe", req.Method())
	assert.Equal(t, "newHeads", req.SubType())
	assert.False(t, req.IsCompleted())
	assert.True(t, req.ShouldDoOnClose())
	assert.Empty(t, req.SubID())
}

func TestRequestRegistryStartForwardsInternalMessages(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "", func(wsupstream.RequestOperation) {})
	registry.Start(req)

	message := &protocol.WsResponse{Id: "request-1", Type: protocol.JsonRpc, Message: []byte(`"0x1"`)}
	req.Write(message, wsupstream.MessageInternal)

	assertSameMessage(t, req.GetChannel(wsupstream.MessageResponse), message)
}

func TestRequestRegistryStartCallsDoOnCloseOnCancel(t *testing.T) {
	loadMethodSpecs(t)

	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	done := make(chan wsupstream.RequestOperation, 1)
	req := registry.Register(context.Background(), request, "request-1", "newHeads", func(op wsupstream.RequestOperation) {
		done <- op
	})
	registry.Start(req)

	subscriptionResponse := &protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0xsub"`),
	}
	registry.OnRpcMessage(subscriptionResponse)
	assertSameMessage(t, req.GetChannel(wsupstream.MessageResponse), subscriptionResponse)

	req.Cancel()

	select {
	case got := <-done:
		assert.Same(t, req, got)
	case <-time.After(time.Second):
		t.Fatal("expected doOnClose to be called")
	}

	require.Eventually(t, req.IsCompleted, time.Second, 10*time.Millisecond)
	assertClosedChannel(t, req.GetChannel(wsupstream.MessageResponse))
}

func TestRequestRegistryStartSkipsDoOnCloseWhenDisabled(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	called := make(chan struct{}, 1)
	req := registry.Register(context.Background(), request, "request-1", "", func(op wsupstream.RequestOperation) {
		called <- struct{}{}
	})
	req.SetSkipDoOnClose()

	registry.Start(req)

	req.Cancel()

	select {
	case <-called:
		t.Fatal("did not expect doOnClose to be called")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRequestRegistryAbortCancelsOperationAndRemovesRequest(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "", func(wsupstream.RequestOperation) {})
	registry.Start(req)

	registry.Abort("request-1")

	require.Eventually(t, func() bool {
		return !req.ShouldDoOnClose()
	}, time.Second, 10*time.Millisecond)
	assertDone(t, req.CtxDone())

	registry.OnRpcMessage(&protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0x1"`),
	})

	assertNoMessage(t, req.GetChannel(wsupstream.MessageResponse))
}

func TestRequestRegistryCancelCancelsUnaryOperationWithoutDoOnClose(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	called := make(chan struct{}, 1)
	req := registry.Register(context.Background(), request, "request-1", "", func(wsupstream.RequestOperation) {
		called <- struct{}{}
	})
	registry.Start(req)

	registry.Cancel("request-1")

	assertDone(t, req.CtxDone())
	require.Eventually(t, req.IsCompleted, time.Second, 10*time.Millisecond)

	select {
	case <-called:
		t.Fatal("did not expect doOnClose for unary cancel")
	case <-time.After(200 * time.Millisecond):
	}

	registry.OnRpcMessage(&protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0x1"`),
	})

	assertNoMessage(t, req.GetChannel(wsupstream.MessageResponse))
}

func TestRequestRegistryCancelCancelsSubscriptionAndDetachesEvents(t *testing.T) {
	loadMethodSpecs(t)

	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	done := make(chan wsupstream.RequestOperation, 1)
	req := registry.Register(context.Background(), request, "request-1", "newHeads", func(op wsupstream.RequestOperation) {
		done <- op
	})
	registry.Start(req)

	subscriptionResponse := &protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0xsub"`),
	}
	registry.OnRpcMessage(subscriptionResponse)
	assertSameMessage(t, req.GetChannel(wsupstream.MessageResponse), subscriptionResponse)

	registry.Cancel("request-1")

	assertDone(t, req.CtxDone())
	require.Eventually(t, req.IsCompleted, time.Second, 10*time.Millisecond)

	select {
	case got := <-done:
		assert.Same(t, req, got)
	case <-time.After(time.Second):
		t.Fatal("expected doOnClose to be called")
	}

	registry.OnSubscriptionMessage(&protocol.WsResponse{
		Type:    protocol.Ws,
		SubId:   "0xsub",
		Message: []byte(`{"number":"0x1"}`),
	})

	assertNoMessage(t, req.GetChannel(wsupstream.MessageResponse))
}

func TestRequestRegistryOnRpcMessageRoutesUnaryResponseOnce(t *testing.T) {
	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "", func(wsupstream.RequestOperation) {})
	registry.Start(req)

	first := &protocol.WsResponse{Id: "request-1", Type: protocol.JsonRpc, Message: []byte(`"0x1"`)}
	registry.OnRpcMessage(first)
	assertSameMessage(t, req.GetChannel(wsupstream.MessageResponse), first)

	registry.OnRpcMessage(&protocol.WsResponse{Id: "request-1", Type: protocol.JsonRpc, Message: []byte(`"0x2"`)})
	assertNoMessage(t, req.GetChannel(wsupstream.MessageResponse))
}

func TestRequestRegistryOnRpcMessageStoresSubscriptionAndRoutesEvents(t *testing.T) {
	loadMethodSpecs(t)

	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "newHeads", func(wsupstream.RequestOperation) {})
	registry.Start(req)

	subscriptionResponse := &protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0xsub"`),
	}
	registry.OnRpcMessage(subscriptionResponse)
	assertSameMessage(t, req.GetChannel(wsupstream.MessageResponse), subscriptionResponse)
	assert.Equal(t, "0xsub", req.SubID())

	event := &protocol.WsResponse{
		Type:    protocol.Ws,
		SubId:   "0xsub",
		Message: []byte(`{"number":"0x1"}`),
	}
	registry.OnSubscriptionMessage(event)
	assertSameMessage(t, req.GetChannel(wsupstream.MessageResponse), event)
}

func TestRequestRegistryOnRpcMessageWithErrorCancelsOperation(t *testing.T) {
	loadMethodSpecs(t)

	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	req := registry.Register(context.Background(), request, "request-1", "newHeads", func(wsupstream.RequestOperation) {})
	registry.Start(req)

	response := &protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`{"message":"error","code":500}`),
		Error:   protocol.ServerError(),
	}
	registry.OnRpcMessage(response)

	assertSameMessage(t, req.GetChannel(wsupstream.MessageResponse), response)
	assertDone(t, req.CtxDone())
}

func TestRequestRegistryCancelAllCancelsRequestsAndSubscriptionsWithoutDoOnClose(t *testing.T) {
	loadMethodSpecs(t)

	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	called := make(chan struct{}, 2)

	unaryRequest, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.ETHEREUM)
	require.NoError(t, err)
	unaryOp := registry.Register(context.Background(), unaryRequest, "request-1", "", func(op wsupstream.RequestOperation) {
		called <- struct{}{}
	})

	subRequest, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)
	subOp := registry.Register(context.Background(), subRequest, "request-2", "newHeads", func(op wsupstream.RequestOperation) {
		called <- struct{}{}
	})

	registry.Start(unaryOp)
	registry.Start(subOp)

	subscriptionResponse := &protocol.WsResponse{
		Id:      "request-2",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0xsub"`),
	}
	registry.OnRpcMessage(subscriptionResponse)
	assertSameMessage(t, subOp.GetChannel(wsupstream.MessageResponse), subscriptionResponse)

	registry.CancelAll()

	require.Eventually(t, func() bool {
		return !unaryOp.ShouldDoOnClose() && !subOp.ShouldDoOnClose()
	}, time.Second, 10*time.Millisecond)
	assertDone(t, unaryOp.CtxDone())
	assertDone(t, subOp.CtxDone())

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

	assertNoMessage(t, unaryOp.GetChannel(wsupstream.MessageResponse))
	assertNoMessage(t, subOp.GetChannel(wsupstream.MessageResponse))
}

func TestRequestRegistrySharedSubscriptionKeepsLastOperationActive(t *testing.T) {
	loadMethodSpecs(t)

	registry := wsupstream.NewBaseRequestRegistry(context.Background(), chains.ETHEREUM, "upstream-1", "eth")
	request1, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)
	request2, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newHeads"}, chains.ETHEREUM)
	require.NoError(t, err)

	var closed atomic.Int32
	op1 := registry.Register(context.Background(), request1, "request-1", "newHeads", func(op wsupstream.RequestOperation) {
		closed.Add(1)
	})
	op2 := registry.Register(context.Background(), request2, "request-2", "newHeads", func(op wsupstream.RequestOperation) {
		closed.Add(1)
	})

	registry.Start(op1)
	registry.Start(op2)

	subscriptionResponse1 := &protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0xsub"`),
	}
	subscriptionResponse2 := &protocol.WsResponse{
		Id:      "request-2",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0xsub"`),
	}
	registry.OnRpcMessage(subscriptionResponse1)
	registry.OnRpcMessage(subscriptionResponse2)

	assertSameMessage(t, op1.GetChannel(wsupstream.MessageResponse), subscriptionResponse1)
	assertSameMessage(t, op2.GetChannel(wsupstream.MessageResponse), subscriptionResponse2)

	op1.Cancel()
	assertDone(t, op1.CtxDone())

	eventAfterFirstCancel := &protocol.WsResponse{
		Type:    protocol.Ws,
		SubId:   "0xsub",
		Message: []byte(`{"number":"0x2"}`),
	}
	registry.OnSubscriptionMessage(eventAfterFirstCancel)
	assertSameMessage(t, op2.GetChannel(wsupstream.MessageResponse), eventAfterFirstCancel)
	assertNoMessage(t, op1.GetChannel(wsupstream.MessageResponse))
	assert.Equal(t, int32(0), closed.Load())

	op2.Cancel()
	assertDone(t, op2.CtxDone())

	eventAfterSecondCancel := &protocol.WsResponse{
		Type:    protocol.Ws,
		SubId:   "0xsub",
		Message: []byte(`{"number":"0x3"}`),
	}
	registry.OnSubscriptionMessage(eventAfterSecondCancel)
	assertNoMessage(t, op2.GetChannel(wsupstream.MessageResponse))
	require.Eventually(t, func() bool {
		return closed.Load() == 1
	}, time.Second, 10*time.Millisecond)
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
