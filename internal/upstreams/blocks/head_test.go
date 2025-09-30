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

func TestRpcHead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	upConfig := config.Upstream{
		ChainName:    "ethereum",
		Id:           "id",
		PollInterval: 10 * time.Millisecond,
	}
	headProcessor := blocks.NewHeadProcessor(ctx, &upConfig, connector, specific.EvmChainSpecific)
	go headProcessor.Start()

	sub := headProcessor.Subscribe("test")

	event, ok := <-sub.Events
	expected := &protocol.BlockData{
		Height: uint64(69195275),
		Hash:   "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18",
	}

	connector.AssertExpectations(t)
	assert.True(t, ok)
	assert.Equal(t, expected, event.HeadData)
	assert.Equal(t, expected, headProcessor.GetCurrentBlock().BlockData)

	headProcessor.UpdateHead(79195275, 0)

	event, ok = <-sub.Events
	expected = &protocol.BlockData{
		Height: uint64(79195275),
	}

	assert.True(t, ok)
	assert.Equal(t, expected, event.HeadData)
	assert.Equal(t, expected, headProcessor.GetCurrentBlock().BlockData)

	headProcessor.UpdateHead(5555, 0)
	go func() {
		time.Sleep(5 * time.Millisecond)
		sub.Unsubscribe()
	}()

	_, ok = <-sub.Events

	assert.False(t, ok)
}

func TestWsHead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connector := mocks.NewWsConnectorMock()
	body := []byte(`{
	  "jsonrpc": "2.0",
	  "method": "eth_subscription",
	  "params": {
		"result": {
		  "number": "0x41fd60b",
		  "hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"
		},
		"subscription": "0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"
	  }
	}`)
	messages := make(chan *protocol.WsResponse, 10)
	messages <- protocol.ParseJsonRpcWsMessage(body)
	response := protocol.NewJsonRpcWsUpstreamResponse(messages)
	connector.On("Subscribe", mock.Anything, mock.Anything).Return(response, nil)

	upConfig := config.Upstream{
		ChainName:    "ethereum",
		Id:           "id",
		PollInterval: 10 * time.Millisecond,
	}
	headProcessor := blocks.NewHeadProcessor(ctx, &upConfig, connector, specific.EvmChainSpecific)
	go headProcessor.Start()

	sub := headProcessor.Subscribe("test")

	event, ok := <-sub.Events
	expected := &protocol.BlockData{
		Height: uint64(69195275),
		Hash:   "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18",
	}

	connector.AssertExpectations(t)
	assert.True(t, ok)
	assert.Equal(t, expected, event.HeadData)
	assert.Equal(t, expected, headProcessor.GetCurrentBlock().BlockData)

	headProcessor.UpdateHead(79195275, 0)

	event, ok = <-sub.Events
	expected = &protocol.BlockData{
		Height: uint64(79195275),
	}

	assert.True(t, ok)
	assert.Equal(t, expected, event.HeadData)
	assert.Equal(t, expected, headProcessor.GetCurrentBlock().BlockData)

	headProcessor.UpdateHead(5555, 0)
	go func() {
		time.Sleep(5 * time.Millisecond)
		sub.Unsubscribe()
	}()

	_, ok = <-sub.Events

	assert.False(t, ok)
}
