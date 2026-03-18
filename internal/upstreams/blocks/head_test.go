package blocks_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/test_utils"
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
		"hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18",
		"parentHash": "0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"
	  }
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)
	connector.On("SendRequest", mock.Anything, mock.Anything).Return(response)

	upConfig := config.Upstream{
		ChainName:    "ethereum",
		Id:           "id",
		PollInterval: 10 * time.Millisecond,
		Options:      &config.UpstreamOptions{InternalTimeout: 5 * time.Second},
	}
	headProcessor := blocks.NewBaseHeadProcessor(ctx, &upConfig, connector, test_utils.NewEvmChainSpecific(connector))
	go headProcessor.Start()

	sub := headProcessor.Subscribe("test")

	event, ok := <-sub.Events
	expected := protocol.Block{
		Height:     uint64(69195275),
		Hash:       blockchain.NewHashIdFromString("0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"),
		ParentHash: blockchain.NewHashIdFromString("0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"),
	}

	connector.AssertExpectations(t)
	assert.True(t, ok)
	assert.Equal(t, expected, event.HeadData)
	assert.Equal(t, expected, headProcessor.GetCurrentBlock())

	headProcessor.UpdateHead(79195275, 0)

	event, ok = <-sub.Events
	expected = protocol.Block{
		Height: uint64(79195275),
	}

	assert.True(t, ok)
	assert.Equal(t, expected, event.HeadData)
	assert.Equal(t, expected, headProcessor.GetCurrentBlock())

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

	reqConnector := mocks.NewConnectorMock()
	bodyLastBlock := []byte(`{
	  "jsonrpc": "2.0",
	  "result": {
		"number": "0x41FD60A",
		"hash": "0x2eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d12",
		"parentHash": "0x3eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d13"
	  }
	}`)
	responseLastBlock := protocol.NewHttpUpstreamResponse("1", bodyLastBlock, 200, protocol.JsonRpc)
	reqConnector.On("SendRequest", mock.Anything, mock.Anything).Return(responseLastBlock)

	connector := mocks.NewWsConnectorMock()
	body := []byte(`{
	  "jsonrpc": "2.0",
	  "method": "eth_subscription",
	  "params": {
		"result": {
		  "number": "0x41fd60b",
		  "hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18",
		  "parentHash": "0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"
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
		Options:      &config.UpstreamOptions{InternalTimeout: 5 * time.Second},
	}
	headProcessor := blocks.NewBaseHeadProcessor(ctx, &upConfig, connector, test_utils.NewEvmChainSpecific(reqConnector))
	go headProcessor.Start()

	sub := headProcessor.Subscribe("test")

	event, ok := <-sub.Events
	expected := protocol.Block{
		Height:     uint64(69195274),
		Hash:       blockchain.NewHashIdFromString("0x2eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d12"),
		ParentHash: blockchain.NewHashIdFromString("0x3eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d13"),
	}
	assert.True(t, ok)
	assert.Equal(t, expected, event.HeadData)
	assert.Equal(t, expected, headProcessor.GetCurrentBlock())

	event, ok = <-sub.Events
	expected = protocol.Block{
		Height:     uint64(69195275),
		Hash:       blockchain.NewHashIdFromString("0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"),
		ParentHash: blockchain.NewHashIdFromString("0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"),
	}

	assert.True(t, ok)
	assert.Equal(t, expected, event.HeadData)
	assert.Equal(t, expected, headProcessor.GetCurrentBlock())

	headProcessor.UpdateHead(79195275, 0)

	event, ok = <-sub.Events
	expected = protocol.Block{
		Height: uint64(79195275),
	}

	assert.True(t, ok)
	assert.Equal(t, expected, event.HeadData)
	assert.Equal(t, expected, headProcessor.GetCurrentBlock())

	headProcessor.UpdateHead(5555, 0)
	go func() {
		time.Sleep(5 * time.Millisecond)
		sub.Unsubscribe()
	}()

	_, ok = <-sub.Events

	assert.False(t, ok)

	connector.AssertExpectations(t)
	reqConnector.AssertExpectations(t)
}
