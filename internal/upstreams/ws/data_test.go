package ws_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRequestFrame(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0"}`)

	frame := ws.NewRequestFrame("request-1", "newHeads", body)

	assert.Equal(t, "request-1", frame.RequestId)
	assert.Equal(t, "newHeads", frame.SubType)
	assert.Equal(t, body, frame.Body)
}

func TestNewBaseRequestOp(t *testing.T) {
	ctx := context.Background()

	op := ws.NewBaseRequestOp(ctx, "eth_subscribe", "newHeads")

	require.NotNil(t, op.GetResponseChannel())
	require.NotNil(t, op.GetInternalChannel())
	assert.Equal(t, "eth_subscribe", op.Method())
	assert.Equal(t, "newHeads", op.SubType())
	assert.False(t, op.IsCompleted())
	assert.Empty(t, op.SubID())
	assert.True(t, op.ShouldDoOnClose())
}

func TestBaseRequestOpWriteResponse(t *testing.T) {
	ctx := context.Background()

	op := ws.NewBaseRequestOp(ctx, "eth_subscribe", "newHeads")
	message := &protocol.WsResponse{Id: "1", Type: protocol.Ws}

	op.WriteResponse(message)

	select {
	case got := <-op.GetResponseChannel():
		assert.Same(t, message, got)
	case <-time.After(time.Second):
		t.Fatal("expected response message")
	}
}

func TestBaseRequestOpWriteInternal(t *testing.T) {
	ctx := context.Background()

	op := ws.NewBaseRequestOp(ctx, "eth_subscribe", "newHeads")
	message := &protocol.WsResponse{Id: "1", Type: protocol.Ws}

	op.WriteInternal(message)

	select {
	case got := <-op.GetInternalChannel():
		assert.Same(t, message, got)
	case <-time.After(time.Second):
		t.Fatal("expected internal message")
	}
}

func TestBaseRequestOpSetSubID(t *testing.T) {
	ctx := context.Background()

	op := ws.NewBaseRequestOp(ctx, "eth_subscribe", "newHeads")

	op.SetSubID("0xsub")

	assert.Equal(t, "0xsub", op.SubID())
}

func TestBaseRequestOpSetSkipDoOnClose(t *testing.T) {
	ctx := context.Background()

	op := ws.NewBaseRequestOp(ctx, "eth_subscribe", "newHeads")

	op.SetSkipDoOnClose()

	assert.False(t, op.ShouldDoOnClose())
}

func TestBaseRequestOpCancelClosesDoneChannel(t *testing.T) {
	ctx := context.Background()
	op := ws.NewBaseRequestOp(ctx, "eth_subscribe", "newHeads")

	op.Cancel()

	select {
	case <-op.Done():
	case <-time.After(time.Second):
		t.Fatal("expected done channel to close")
	}
}

func TestBaseRequestOpCompleteMarksCompletedAndClosesResponseChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	op := ws.NewBaseRequestOp(ctx, "eth_subscribe", "newHeads")

	op.Cancel()

	assert.True(t, op.IsCompleted())

	select {
	case _, ok := <-op.GetResponseChannel():
		assert.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("expected response channel to close")
	}
	assert.Eventually(t, op.IsCompleted, 1*time.Second, 50*time.Millisecond)
}
