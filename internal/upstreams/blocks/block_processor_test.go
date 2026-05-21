package blocks_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEthLikeBlockProcessorGetFinalizedBlock(t *testing.T) {
	upConfig := &config.Upstream{Id: "1", PollInterval: 1 * time.Second, Options: &chains.Options{InternalTimeout: 5 * time.Second}}
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
	  "jsonrpc": "2.0",
	  "result": {
		"number": "0x41fd60b",
		"hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18",
		"parentHash": "0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"
	  }
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", mock.Anything, mock.Anything).Return(response)

	processor := blocks.NewEthLikeBlockProcessor(ctx, upConfig, connector, test_utils.NewEvmChainSpecific(connector), false)
	go processor.Start()

	sub := processor.Subscribe("sub")
	event, ok := <-sub.Events

	expected := blocks.BlockEvent{
		Block: protocol.Block{
			Height:     uint64(69195275),
			Hash:       blockchain.NewHashIdFromString("0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"),
			ParentHash: blockchain.NewHashIdFromString("0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"),
		},
		BlockType: protocol.FinalizedBlock,
	}

	connector.AssertExpectations(t)
	assert.True(t, ok)
	assert.Equal(t, expected, event)
	assert.True(t, processor.DisabledBlocks().IsEmpty())

	processor.UpdateBlock(protocol.NewBlockWithHeight(79195275), protocol.FinalizedBlock)

	event, ok = <-sub.Events

	expected = blocks.BlockEvent{
		Block: protocol.Block{
			Height: uint64(79195275),
		},
		BlockType: protocol.FinalizedBlock,
	}

	assert.True(t, ok)
	assert.Equal(t, expected, event)
	assert.True(t, processor.DisabledBlocks().IsEmpty())
}

func TestEthLikeBlockProcessorDisableFinalizedBlock(t *testing.T) {
	upConfig := &config.Upstream{Id: "1", PollInterval: 1 * time.Second, Options: &chains.Options{InternalTimeout: 5 * time.Second}}
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
	  "jsonrpc": "2.0",
	  "error": {
		"code": 1,
		"message": "got an invalid block number"
	  }
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", mock.Anything, mock.Anything).Return(response)

	processor := blocks.NewEthLikeBlockProcessor(ctx, upConfig, connector, test_utils.NewEvmChainSpecific(connector), false)
	go processor.Start()

	sub := processor.Subscribe("sub")
	go func() {
		time.Sleep(10 * time.Millisecond)
		sub.Unsubscribe()
	}()
	_, ok := <-sub.Events

	connector.AssertExpectations(t)
	assert.False(t, ok)
	assert.True(t, processor.DisabledBlocks().Contains(protocol.FinalizedBlock))
}

func TestEthLikeBlockProcessorSkipsFinalizedPollWhenNoFinality(t *testing.T) {
	upConfig := &config.Upstream{Id: "1", PollInterval: 50 * time.Millisecond, Options: &chains.Options{InternalTimeout: 5 * time.Second}}
	ctx := context.Background()
	connector := mocks.NewConnectorMock()

	// The processor must never call SendRequest when noFinality is true. If
	// the goroutine still polls FinalizedBlock the mock will assert-fail
	// because no expectations were registered.
	processor := blocks.NewEthLikeBlockProcessor(ctx, upConfig, connector, test_utils.NewEvmChainSpecific(connector), true)
	go processor.Start()

	sub := processor.Subscribe("sub")
	go func() {
		time.Sleep(150 * time.Millisecond)
		sub.Unsubscribe()
	}()
	_, ok := <-sub.Events

	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
	assert.False(t, ok)
	assert.True(t, processor.DisabledBlocks().IsEmpty())
}
