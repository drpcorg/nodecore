package blocks

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingSpecific hands out increasing heights: GetLatestBlock for the polling head,
// ParseSubscriptionBlock for the subscription head. The other calls are never reached.
type countingSpecific struct {
	stubSpecific
	height     atomic.Uint64
	failLatest bool
}

func (c *countingSpecific) GetLatestBlock(context.Context) (protocol.Block, error) {
	if c.failLatest {
		return protocol.ZeroBlock{}, errors.New("no latest block")
	}
	return protocol.NewBlockWithHeight(c.height.Add(1)), nil
}

func (c *countingSpecific) ParseSubscriptionBlock([]byte) (protocol.Block, error) {
	return protocol.NewBlockWithHeight(c.height.Add(1)), nil
}

// subStubConnector serves one subscription response, shared across Subscribe calls, so a
// restarted head keeps reading from the same message channel.
type subStubConnector struct {
	stubConnector
	response protocol.UpstreamSubscriptionResponse
}

func (s *subStubConnector) Subscribe(context.Context, protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return s.response, nil
}

func nextBlock(t *testing.T, heads <-chan protocol.Block) protocol.Block {
	t.Helper()
	select {
	case block := <-heads:
		return block
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a head block")
		return protocol.ZeroBlock{}
	}
}

func TestRpcHeadStopReleasesPollParkedOnSend(t *testing.T) {
	specific := &countingSpecific{}
	head := NewRpcHead(context.Background(), "up", time.Second, time.Hour, specific)

	// nobody reads the heads: the first poll parks in its send
	head.Start()
	require.Eventually(t, func() bool { return specific.height.Load() == 1 }, time.Second, time.Millisecond)

	// a stop must release it and hand back the poll slot
	head.Stop()
	require.Eventually(t, func() bool { return !head.pollInProgress.Load() }, time.Second, time.Millisecond,
		"the parked poll was not released by Stop")

	// the restarted head delivers a fresh poll, not the block captured before the stop
	head.Start()
	defer head.Stop()
	assert.Equal(t, uint64(2), nextBlock(t, head.HeadsChan()).Height)
}

func TestSubscriptionHeadStopReleasesProducerParkedOnSend(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"result":{"number":"0x1"},"subscription":"0x1"}}`)
	messages := make(chan protocol.SubResponse, 2)
	messages <- protocol.ParseJsonRpcWsMessage(body)
	messages <- protocol.ParseJsonRpcWsMessage(body)
	connector := &subStubConnector{response: protocol.NewJsonRpcWsUpstreamResponse(messages, "op-1")}
	specific := &countingSpecific{failLatest: true} // only the subscription produces heads
	head := NewSubHead(context.Background(), "up", time.Second, connector, specific)

	// nobody reads the heads: the producer parks in its send with the first block
	head.Start()
	require.Eventually(t, func() bool { return specific.height.Load() == 1 }, time.Second, time.Millisecond)

	head.Stop()

	// the restarted head delivers the next message, not the block captured before the stop
	head.Start()
	defer head.Stop()
	assert.Equal(t, uint64(2), nextBlock(t, head.HeadsChan()).Height)
}
