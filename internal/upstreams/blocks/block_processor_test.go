package blocks_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEthLikeBlockProcessorGetFinalizedBlock(t *testing.T) {
	upConfig := &config.Upstream{Id: "1", PollInterval: 1 * time.Second}
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
	  "jsonrpc": "2.0",
	  "result": {
		"number": "0x41fd60b",
		"hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"
	  }
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", mock.Anything, mock.Anything).Return(response)

	processor := blocks.NewEthLikeBlockProcessor(ctx, upConfig, connector, specific.EvmChainSpecific)
	go processor.Start()

	sub := processor.Subscribe("sub")
	event, ok := <-sub.Events

	expected := blocks.BlockEvent{
		BlockData: &protocol.BlockData{
			Height: uint64(69195275),
			Hash:   "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18",
		},
		BlockType: protocol.FinalizedBlock,
	}

	connector.AssertExpectations(t)
	assert.True(t, ok)
	assert.Equal(t, expected, event)
	assert.True(t, processor.DisabledBlocks().IsEmpty())
}

func TestEthLikeBlockProcessorDisableFinalizedBlock(t *testing.T) {
	upConfig := &config.Upstream{Id: "1", PollInterval: 1 * time.Second}
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

	processor := blocks.NewEthLikeBlockProcessor(ctx, upConfig, connector, specific.EvmChainSpecific)
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
