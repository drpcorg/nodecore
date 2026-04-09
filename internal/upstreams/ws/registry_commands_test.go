package ws

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var loadRegistryCommandSpecsOnce sync.Once

func TestRegisterCommandHandleStoresRequest(t *testing.T) {
	registry := newTestRegistryState("eth")
	req := NewBaseRequestOp(context.Background(), "request-1", "eth_blockNumber", "", func(RequestOperation) {})

	newRegisterCommand("request-1", req).handle(registry)

	assert.Same(t, req, registry.registryState.requests["request-1"])
}

func TestAbortCommandHandleCancelsRequestAndSkipsDoOnClose(t *testing.T) {
	registry := newTestRegistryState("eth")
	req := NewBaseRequestOp(context.Background(), "request-1", "eth_blockNumber", "", func(RequestOperation) {})
	registry.registryState.requests["request-1"] = req

	newAbortCommand("request-1").handle(registry)

	assert.NotContains(t, registry.registryState.requests, "request-1")
	assert.False(t, req.ShouldDoOnClose())
	assertDoneRegistryCommand(t, req.CtxDone())
}

func TestRPCCommandHandleDropsUnaryMessageWhenInternalChannelIsFull(t *testing.T) {
	registry := newTestRegistryState("eth")
	req := NewBaseRequestOp(context.Background(), "request-1", "eth_blockNumber", "", func(RequestOperation) {})
	registry.registryState.requests["request-1"] = req
	fillInternalChannel(t, req)

	response := &protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0x1"`),
	}
	done := make(chan struct{})
	go func() {
		newRpcCommand(response).handle(registry)
		close(done)
	}()

	assertCommandCompletes(t, done)

	assert.NotContains(t, registry.registryState.requests, "request-1")
	assert.Len(t, req.GetChannel(MessageInternal), cap(req.GetChannel(MessageInternal)))
}

func TestRPCCommandHandleStoresSubscriptionAndDropsMessageWhenInternalChannelIsFull(t *testing.T) {
	loadRegistryCommandMethodSpecs(t)

	registry := newTestRegistryState("eth")
	req := NewBaseRequestOp(context.Background(), "request-1", "eth_subscribe", "newHeads", func(RequestOperation) {})
	registry.registryState.requests["request-1"] = req
	fillInternalChannel(t, req)

	response := &protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0xsub"`),
	}
	done := make(chan struct{})
	go func() {
		newRpcCommand(response).handle(registry)
		close(done)
	}()

	assertCommandCompletes(t, done)

	assert.Equal(t, "0xsub", req.SubID())
	require.Contains(t, registry.registryState.subs, "0xsub")
	assert.Contains(t, registry.registryState.subs["0xsub"].ops, req.Id())
	assert.Len(t, req.GetChannel(MessageInternal), cap(req.GetChannel(MessageInternal)))
}

func TestSubscriptionCommandHandleDropsMessagesWhenInternalChannelsAreFull(t *testing.T) {
	registry := newTestRegistryState("eth")
	req1 := NewBaseRequestOp(context.Background(), "request-1", "eth_subscribe", "newHeads", func(RequestOperation) {})
	req2 := NewBaseRequestOp(context.Background(), "request-2", "eth_subscribe", "newHeads", func(RequestOperation) {})
	req1.SetSubID([]byte(`"0xsub"`))
	req2.SetSubID([]byte(`"0xsub"`))
	registry.registryState.subs["0xsub"] = &registrySubscription{
		subType: "newHeads",
		ops: map[string]RequestOperation{
			req1.Id(): req1,
			req2.Id(): req2,
		},
	}
	fillInternalChannel(t, req1)
	fillInternalChannel(t, req2)

	event := &protocol.WsResponse{
		Type:    protocol.Ws,
		SubId:   "0xsub",
		Message: []byte(`{"number":"0x1"}`),
	}
	done := make(chan struct{})
	go func() {
		newSubscriptionCommand(event).handle(registry)
		close(done)
	}()

	assertCommandCompletes(t, done)

	assert.Len(t, req1.GetChannel(MessageInternal), cap(req1.GetChannel(MessageInternal)))
	assert.Len(t, req2.GetChannel(MessageInternal), cap(req2.GetChannel(MessageInternal)))
}

func TestFinishCommandHandleKeepsSharedSubscriptionUntilLastRequest(t *testing.T) {
	registry := newTestRegistryState("eth")
	req1 := NewBaseRequestOp(context.Background(), "request-1", "eth_subscribe", "newHeads", func(RequestOperation) {})
	req2 := NewBaseRequestOp(context.Background(), "request-2", "eth_subscribe", "newHeads", func(RequestOperation) {})
	req1.SetSubID([]byte(`"0xsub"`))
	req2.SetSubID([]byte(`"0xsub"`))
	registry.registryState.subs["0xsub"] = &registrySubscription{
		subType: "newHeads",
		ops: map[string]RequestOperation{
			req1.Id(): req1,
			req2.Id(): req2,
		},
	}

	firstResult := make(chan bool, 1)
	newFinishCommand(req1, firstResult).handle(registry)

	assert.False(t, <-firstResult)
	assertDoneRegistryCommand(t, req1.CtxDone())
	require.Contains(t, registry.registryState.subs, "0xsub")
	assert.NotContains(t, registry.registryState.subs["0xsub"].ops, req1.Id())
	assert.Contains(t, registry.registryState.subs["0xsub"].ops, req2.Id())

	secondResult := make(chan bool, 1)
	newFinishCommand(req2, secondResult).handle(registry)

	assert.True(t, <-secondResult)
	assertDoneRegistryCommand(t, req2.CtxDone())
	assert.NotContains(t, registry.registryState.subs, "0xsub")
}

func TestFinishCommandHandleReturnsFalseWhenSubscriptionIsMissing(t *testing.T) {
	registry := newTestRegistryState("eth")
	req := NewBaseRequestOp(context.Background(), "request-1", "eth_subscribe", "newHeads", func(RequestOperation) {})
	req.SetSubID([]byte(`"0xmissing"`))
	result := make(chan bool, 1)

	newFinishCommand(req, result).handle(registry)

	assert.False(t, <-result)
	assertDoneRegistryCommand(t, req.CtxDone())
}

func TestCancelAllCommandHandleCancelsRequestsAndSubscriptions(t *testing.T) {
	registry := newTestRegistryState("eth")
	unaryReq := NewBaseRequestOp(context.Background(), "request-1", "eth_blockNumber", "", func(RequestOperation) {})
	subReq1 := NewBaseRequestOp(context.Background(), "request-2", "eth_subscribe", "newHeads", func(RequestOperation) {})
	subReq2 := NewBaseRequestOp(context.Background(), "request-3", "eth_subscribe", "newHeads", func(RequestOperation) {})
	subReq1.SetSubID([]byte(`"0xsub"`))
	subReq2.SetSubID([]byte(`"0xsub"`))
	registry.registryState.requests["request-1"] = unaryReq
	registry.registryState.subs["0xsub"] = &registrySubscription{
		subType: "newHeads",
		ops: map[string]RequestOperation{
			subReq1.Id(): subReq1,
			subReq2.Id(): subReq2,
		},
	}

	newCancelAllCommand().handle(registry)

	assert.Empty(t, registry.registryState.requests)
	assert.Empty(t, registry.registryState.subs)
	assert.False(t, unaryReq.ShouldDoOnClose())
	assert.False(t, subReq1.ShouldDoOnClose())
	assert.False(t, subReq2.ShouldDoOnClose())
	assertDoneRegistryCommand(t, unaryReq.CtxDone())
	assertDoneRegistryCommand(t, subReq1.CtxDone())
	assertDoneRegistryCommand(t, subReq2.CtxDone())
}

func fillInternalChannel(t *testing.T, req *BaseRequestOp) {
	t.Helper()

	internal := req.GetChannel(MessageInternal)
	for i := 0; i < cap(internal); i++ {
		select {
		case internal <- &protocol.WsResponse{Id: "filler"}:
		case <-time.After(time.Second):
			t.Fatal("failed to fill internal channel")
		}
	}
}

func assertCommandCompletes(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("command did not return after WriteInternal completed")
	}
}

func assertDoneRegistryCommand(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected done channel to close")
	}
}

func newTestRegistryState(methodSpec string) *BaseRequestRegistry {
	return &BaseRequestRegistry{
		chain:      chains.ETHEREUM,
		upId:       "upstream-1",
		methodSpec: methodSpec,
		registryState: &registryState{
			requests: make(map[string]RequestOperation),
			subs:     make(map[string]*registrySubscription),
		},
	}
}

func loadRegistryCommandMethodSpecs(t *testing.T) {
	t.Helper()

	loadRegistryCommandSpecsOnce.Do(func() {
		require.NoError(t, specs.NewMethodSpecLoader().Load())
	})
}
