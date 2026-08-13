package evm_caps_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/caps/evm_caps"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// stateFeed wires a ConnectorMock so SubscribeStates returns a subscription the test
// can publish into.
func stateFeed(name string) (*mocks.ConnectorMock, *utils.SubscriptionManager[protocol.SubscribeConnectorState]) {
	mgr := utils.NewSubscriptionManager[protocol.SubscribeConnectorState]("test_states")
	sub := mgr.Subscribe(name)
	conn := mocks.NewConnectorMock()
	conn.On("SubscribeStates", name).Return(sub)
	return conn, mgr
}

// txpoolResult builds a txpool_content result with `pending` sender entries under
// "pending" and `queued` under "queued".
func txpoolResult(pending, queued int) []byte {
	var b strings.Builder
	b.WriteString(`{"pending":{`)
	writeAddrs(&b, 0, pending)
	b.WriteString(`},"queued":{`)
	writeAddrs(&b, 1_000_000, queued)
	b.WriteString(`}}`)
	return []byte(b.String())
}

func writeAddrs(b *strings.Builder, offset, n int) {
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(b, `"0x%040x":{}`, offset+i)
	}
}

func TestEvmPendingTxCapDetector(t *testing.T) {
	t.Run("asserts PendingTxCap when txpool is at the limit and ws is up", func(t *testing.T) {
		wsConn, mgr := stateFeed("pt")
		internalConn := mocks.NewConnectorMock()
		internalConn.On("SendRequest", mock.Anything, mock.Anything).
			Return(protocol.NewSimpleHttpUpstreamResponse("1", txpoolResult(600, 400), protocol.JsonRpc))

		detector := evm_caps.NewEvmPendingTxCapDetector("pt", wsConn, internalConn, chains.BASE, time.Second, evm_caps.BaseTxLimit)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		mgr.Publish(protocol.WsConnected)
		assert.True(t, (<-out).Contains(protocol.PendingTxCap))
	})

	t.Run("does not assert when txpool is below the limit", func(t *testing.T) {
		wsConn, mgr := stateFeed("pt")
		internalConn := mocks.NewConnectorMock()
		internalConn.On("SendRequest", mock.Anything, mock.Anything).
			Return(protocol.NewSimpleHttpUpstreamResponse("1", txpoolResult(500, 499), protocol.JsonRpc))

		detector := evm_caps.NewEvmPendingTxCapDetector("pt", wsConn, internalConn, chains.BASE, time.Second, evm_caps.BaseTxLimit)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		mgr.Publish(protocol.WsConnected)
		assert.False(t, (<-out).Contains(protocol.PendingTxCap))
	})

	t.Run("missing pending/queued keys count as zero", func(t *testing.T) {
		wsConn, mgr := stateFeed("pt")
		internalConn := mocks.NewConnectorMock()
		internalConn.On("SendRequest", mock.Anything, mock.Anything).
			Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{}`), protocol.JsonRpc))

		detector := evm_caps.NewEvmPendingTxCapDetector("pt", wsConn, internalConn, chains.BASE, time.Second, evm_caps.BaseTxLimit)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		mgr.Publish(protocol.WsConnected)
		assert.False(t, (<-out).Contains(protocol.PendingTxCap))
	})

	t.Run("rpc error keeps the cap off", func(t *testing.T) {
		wsConn, mgr := stateFeed("pt")
		internalConn := mocks.NewConnectorMock()
		internalConn.On("SendRequest", mock.Anything, mock.Anything).
			Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

		detector := evm_caps.NewEvmPendingTxCapDetector("pt", wsConn, internalConn, chains.BASE, time.Second, evm_caps.BaseTxLimit)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		mgr.Publish(protocol.WsConnected)
		assert.False(t, (<-out).Contains(protocol.PendingTxCap))
	})

	t.Run("drops the cap on disconnect even with a full mempool", func(t *testing.T) {
		wsConn, mgr := stateFeed("pt")
		internalConn := mocks.NewConnectorMock()
		internalConn.On("SendRequest", mock.Anything, mock.Anything).
			Return(protocol.NewSimpleHttpUpstreamResponse("1", txpoolResult(1000, 0), protocol.JsonRpc))

		detector := evm_caps.NewEvmPendingTxCapDetector("pt", wsConn, internalConn, chains.BASE, time.Second, evm_caps.BaseTxLimit)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		mgr.Publish(protocol.WsConnected)
		assert.True(t, (<-out).Contains(protocol.PendingTxCap))

		mgr.Publish(protocol.WsDisconnected)
		assert.False(t, (<-out).Contains(protocol.PendingTxCap))
	})

	t.Run("no ws connector never asserts the cap", func(t *testing.T) {
		detector := evm_caps.NewEvmPendingTxCapDetector("pt", nil, mocks.NewConnectorMock(), chains.BASE, time.Second, evm_caps.BaseTxLimit)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		_, ok := <-out
		assert.False(t, ok)
	})
}

func TestEvmHeadSubCapDetector(t *testing.T) {
	t.Run("ws head with eth_subscribe and eth_getLogs asserts NewHeads and Logs", func(t *testing.T) {
		headConn, mgr := stateFeed("head")
		methods := mocks.NewMethodsMock()
		methods.On("HasMethod", "eth_subscribe").Return(true)
		methods.On("HasMethod", "eth_getLogs").Return(true)

		detector := evm_caps.NewEvmHeadSubCapDetector("head", headConn, methods)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		mgr.Publish(protocol.WsConnected)
		got := <-out
		assert.True(t, got.Contains(protocol.NewHeadsCap))
		assert.True(t, got.Contains(protocol.LogsCap))
	})

	t.Run("ws head without eth_getLogs asserts only NewHeads", func(t *testing.T) {
		headConn, mgr := stateFeed("head")
		methods := mocks.NewMethodsMock()
		methods.On("HasMethod", "eth_subscribe").Return(true)
		methods.On("HasMethod", "eth_getLogs").Return(false)

		detector := evm_caps.NewEvmHeadSubCapDetector("head", headConn, methods)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		mgr.Publish(protocol.WsConnected)
		got := <-out
		assert.True(t, got.Contains(protocol.NewHeadsCap))
		assert.False(t, got.Contains(protocol.LogsCap))
	})

	t.Run("without eth_subscribe asserts nothing", func(t *testing.T) {
		headConn, mgr := stateFeed("head")
		methods := mocks.NewMethodsMock()
		methods.On("HasMethod", "eth_subscribe").Return(false)

		detector := evm_caps.NewEvmHeadSubCapDetector("head", headConn, methods)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		mgr.Publish(protocol.WsConnected)
		got := <-out
		assert.Equal(t, 0, got.Cardinality())
	})

	t.Run("non-ws head connector asserts nothing", func(t *testing.T) {
		headConn := mocks.NewConnectorMock()
		headConn.On("SubscribeStates", "head").Return(nil)

		detector := evm_caps.NewEvmHeadSubCapDetector("head", headConn, mocks.NewMethodsMock())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := detector.DetectCaps(ctx)

		_, ok := <-out
		assert.False(t, ok)
	})
}
