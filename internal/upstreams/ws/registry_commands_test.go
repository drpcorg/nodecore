package ws

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterCommandHandleStoresRequest(t *testing.T) {
	registry := newTestRegistryState("eth")
	req := NewGenericRequestOp(context.Background(), "request-1", "eth_blockNumber", "", func(RequestOperation) {})

	newRegisterCommand("request-1", req).handle(registry)

	assert.Same(t, req, registry.registryState.requests["request-1"])
}

func TestAbortCommandHandleCancelsRequestAndSkipsDoOnClose(t *testing.T) {
	registry := newTestRegistryState("eth")
	req := NewGenericRequestOp(context.Background(), "request-1", "eth_blockNumber", "", func(RequestOperation) {})
	registry.registryState.requests["request-1"] = req

	newAbortCommand("request-1").handle(registry)

	assert.NotContains(t, registry.registryState.requests, "request-1")
	assert.False(t, req.ShouldDoOnClose())
	assertDoneRegistryCommand(t, req.CtxDone())
}

func TestRPCCommandHandleDropsUnaryMessageWhenInternalChannelIsFull(t *testing.T) {
	registry := newTestRegistryState("eth")
	req := NewGenericRequestOp(context.Background(), "request-1", "eth_blockNumber", "", func(RequestOperation) {})
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

func TestRPCCommandHandleSwallowsSuccessfulSubscribeConfirmation(t *testing.T) {
	loadRegistryCommandMethodSpecs(t)

	registry := newTestRegistryState("eth")
	req := NewGenericRequestOp(context.Background(), "request-1", "eth_subscribe", "newHeads", func(RequestOperation) {})
	registry.registryState.requests["request-1"] = req

	newRpcCommand(&protocol.WsResponse{
		Id:      "request-1",
		Type:    protocol.JsonRpc,
		Message: []byte(`"0xsub"`),
	}).handle(registry)

	// the confirmation's only job is the bookkeeping...
	assert.Equal(t, "0xsub", req.SubID())
	require.Contains(t, registry.registryState.subs, "0xsub")
	assert.Contains(t, registry.registryState.subs["0xsub"].ops, req.Id())
	// ...and it must NOT be forwarded to the op
	assert.Empty(t, req.GetChannel(MessageInternal))
}

func TestRPCCommandHandleForwardsSubscribeErrorConfirmation(t *testing.T) {
	loadRegistryCommandMethodSpecs(t)

	registry := newTestRegistryState("eth")
	req := NewGenericRequestOp(context.Background(), "request-1", "eth_subscribe", "newHeads", func(RequestOperation) {})
	registry.registryState.requests["request-1"] = req

	resp := &protocol.WsResponse{
		Id:    "request-1",
		Type:  protocol.JsonRpc,
		Error: protocol.ResponseErrorWithMessage("subscribe rejected"),
	}
	newRpcCommand(resp).handle(registry)

	require.Len(t, req.GetChannel(MessageInternal), 1)
	assert.Same(t, resp, <-req.GetChannel(MessageInternal))
}

func TestSubscriptionCommandHandleDropsMessagesWhenInternalChannelsAreFull(t *testing.T) {
	registry := newTestRegistryState("eth")
	req1 := NewGenericRequestOp(context.Background(), "request-1", "eth_subscribe", "newHeads", func(RequestOperation) {})
	req2 := NewGenericRequestOp(context.Background(), "request-2", "eth_subscribe", "newHeads", func(RequestOperation) {})
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
	req1 := NewGenericRequestOp(context.Background(), "request-1", "eth_subscribe", "newHeads", func(RequestOperation) {})
	req2 := NewGenericRequestOp(context.Background(), "request-2", "eth_subscribe", "newHeads", func(RequestOperation) {})
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
	req := NewGenericRequestOp(context.Background(), "request-1", "eth_subscribe", "newHeads", func(RequestOperation) {})
	req.SetSubID([]byte(`"0xmissing"`))
	result := make(chan bool, 1)

	newFinishCommand(req, result).handle(registry)

	assert.False(t, <-result)
	assertDoneRegistryCommand(t, req.CtxDone())
}

func TestCancelAllCommandHandleCancelsRequestsAndSubscriptions(t *testing.T) {
	registry := newTestRegistryState("eth")
	unaryReq := NewGenericRequestOp(context.Background(), "request-1", "eth_blockNumber", "", func(RequestOperation) {})
	subReq1 := NewGenericRequestOp(context.Background(), "request-2", "eth_subscribe", "newHeads", func(RequestOperation) {})
	subReq2 := NewGenericRequestOp(context.Background(), "request-3", "eth_subscribe", "newHeads", func(RequestOperation) {})
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

func fillInternalChannel(t *testing.T, req *GenericRequestOp) {
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

func newTestRegistryState(methodSpec string) *GenericRequestRegistry {
	return &GenericRequestRegistry{
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

	specs_utils.LoadMethodSpecs()
}

// A node-side end (a go-jsonrpc xrpc.ch.close, parsed as a Ws frame carrying
// the total-failure error) reaches every op of the sub and drops the sub, so
// the ops' finish does not run the close hook: there is nothing left on the
// node to cancel.
func TestSubscriptionCommandHandleNodeEndDropsTheSubscription(t *testing.T) {
	registry := newTestRegistryState("celestia")
	req1 := NewGenericRequestOp(context.Background(), "101", "header.Subscribe", "header.Subscribe", func(RequestOperation) {})
	req2 := NewGenericRequestOp(context.Background(), "102", "header.Subscribe", "header.Subscribe", func(RequestOperation) {})
	req1.SetSubID([]byte(`7`))
	req2.SetSubID([]byte(`7`))
	registry.registryState.subs["7"] = &registrySubscription{
		subType: "header.Subscribe",
		ops: map[string]RequestOperation{
			req1.Id(): req1,
			req2.Id(): req2,
		},
	}

	end := &protocol.WsResponse{Type: protocol.Ws, SubId: "7", Error: protocol.SubscribeTotalFailureError()}
	newSubscriptionCommand(end).handle(registry)

	for _, req := range []*GenericRequestOp{req1, req2} {
		select {
		case got := <-req.GetChannel(MessageInternal):
			assert.Equal(t, protocol.SubscribeTotalFailureError(), got.GetError())
		case <-time.After(time.Second):
			t.Fatalf("op %s did not receive the end frame", req.Id())
		}
	}
	assert.NotContains(t, registry.registryState.subs, "7")

	result := make(chan bool, 1)
	newFinishCommand(req1, result).handle(registry)
	assert.False(t, <-result)
}

// A plain event keeps the subscription filed.
func TestSubscriptionCommandHandleEventKeepsTheSubscription(t *testing.T) {
	registry := newTestRegistryState("celestia")
	req := NewGenericRequestOp(context.Background(), "101", "header.Subscribe", "header.Subscribe", func(RequestOperation) {})
	req.SetSubID([]byte(`7`))
	registry.registryState.subs["7"] = &registrySubscription{
		subType: "header.Subscribe",
		ops:     map[string]RequestOperation{req.Id(): req},
	}

	newSubscriptionCommand(&protocol.WsResponse{Type: protocol.Ws, SubId: "7", Message: []byte(`{}`)}).handle(registry)

	assert.Contains(t, registry.registryState.subs, "7")
	select {
	case got := <-req.GetChannel(MessageInternal):
		assert.Nil(t, got.GetError())
	case <-time.After(time.Second):
		t.Fatal("op did not receive the event")
	}
}
