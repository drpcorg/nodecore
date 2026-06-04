package blocks_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
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

	processor := blocks.NewEthLikeBlockProcessor(ctx, upConfig.Id, upConfig.PollInterval, upConfig.Options.InternalTimeout, false, connector, test_utils.NewEvmChainSpecific(connector))
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

	var manualEvent blocks.BlockEvent
	for i := 0; i < 2; i++ {
		event, ok = <-sub.Events
		assert.True(t, ok)
		if event.BlockType == protocol.FinalizedBlock && event.Block.Height == 79195275 {
			manualEvent = event
			break
		}
	}

	expected = blocks.BlockEvent{
		Block: protocol.Block{
			Height: uint64(79195275),
		},
		BlockType: protocol.FinalizedBlock,
	}

	assert.Equal(t, expected, manualEvent)
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

	processor := blocks.NewEthLikeBlockProcessor(ctx, upConfig.Id, upConfig.PollInterval, upConfig.Options.InternalTimeout, false, connector, test_utils.NewEvmChainSpecific(connector))
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

func TestEthLikeBlockProcessorPollsSafeBlockWhenSupported(t *testing.T) {
	disableSafe := false
	upConfig := &config.Upstream{Id: "1", PollInterval: time.Hour, Options: &chains.Options{InternalTimeout: 5 * time.Second, DisableSafeBlockDetection: &disableSafe}}
	chainSpecific := &blockProcessorChainSpecificStub{
		blockProcessorChainSpecificNoSafeStub: blockProcessorChainSpecificNoSafeStub{finalized: protocol.NewBlockWithHeight(100)},
		safe:                                  protocol.NewBlockWithHeight(90),
	}
	processor := blocks.NewEthLikeBlockProcessor(context.Background(), upConfig.Id, upConfig.PollInterval, upConfig.Options.InternalTimeout, *upConfig.Options.DisableSafeBlockDetection, mocks.NewConnectorMock(), chainSpecific)
	sub := processor.Subscribe("sub")

	go processor.Start()

	seen := map[protocol.BlockType]protocol.Block{}
	deadline := time.After(time.Second)
	for len(seen) < 2 {
		select {
		case event := <-sub.Events:
			seen[event.BlockType] = event.Block
		case <-deadline:
			t.Fatalf("timed out waiting for finalized and safe block events; seen=%v", seen)
		}
	}

	assert.Equal(t, uint64(100), seen[protocol.FinalizedBlock].Height)
	assert.Equal(t, uint64(90), seen[protocol.SafeBlock].Height)
	assert.True(t, processor.DisabledBlocks().IsEmpty())
}

func TestEthLikeBlockProcessorDisablesSafeBlockWhenUnsupported(t *testing.T) {
	disableSafe := false
	upConfig := &config.Upstream{Id: "1", PollInterval: time.Hour, Options: &chains.Options{InternalTimeout: 5 * time.Second, DisableSafeBlockDetection: &disableSafe}}
	chainSpecific := &blockProcessorChainSpecificNoSafeStub{finalized: protocol.NewBlockWithHeight(100)}
	processor := blocks.NewEthLikeBlockProcessor(context.Background(), upConfig.Id, upConfig.PollInterval, upConfig.Options.InternalTimeout, *upConfig.Options.DisableSafeBlockDetection, mocks.NewConnectorMock(), chainSpecific)
	sub := processor.Subscribe("sub")

	go processor.Start()

	select {
	case event := <-sub.Events:
		assert.Equal(t, protocol.FinalizedBlock, event.BlockType)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for finalized block event")
	}

	assert.Eventually(t, func() bool {
		return processor.DisabledBlocks().Contains(protocol.SafeBlock)
	}, time.Second, 10*time.Millisecond)
}

type blockProcessorChainSpecificNoSafeStub struct {
	finalized protocol.Block
}

func (b *blockProcessorChainSpecificNoSafeStub) GetLatestBlock(context.Context) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (b *blockProcessorChainSpecificNoSafeStub) GetFinalizedBlock(context.Context) (protocol.Block, error) {
	return b.finalized, nil
}

func (b *blockProcessorChainSpecificNoSafeStub) ParseBlock([]byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (b *blockProcessorChainSpecificNoSafeStub) ParseSubscriptionBlock([]byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (b *blockProcessorChainSpecificNoSafeStub) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, nil
}

func (b *blockProcessorChainSpecificNoSafeStub) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	return nil
}

func (b *blockProcessorChainSpecificNoSafeStub) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	return nil
}

func (b *blockProcessorChainSpecificNoSafeStub) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	return nil
}

func (b *blockProcessorChainSpecificNoSafeStub) LabelsProcessor() labels.LabelsProcessor {
	return nil
}

type blockProcessorChainSpecificStub struct {
	blockProcessorChainSpecificNoSafeStub
	safe protocol.Block
}

func (b *blockProcessorChainSpecificStub) GetSafeBlock(context.Context) (protocol.Block, error) {
	return b.safe, nil
}

func TestEthLikeBlockProcessorSkipsSafeBlockWhenDisabled(t *testing.T) {
	disableSafe := true
	upConfig := &config.Upstream{Id: "1", PollInterval: time.Hour, Options: &chains.Options{InternalTimeout: 5 * time.Second, DisableSafeBlockDetection: &disableSafe}}
	chainSpecific := &blockProcessorChainSpecificStub{
		blockProcessorChainSpecificNoSafeStub: blockProcessorChainSpecificNoSafeStub{finalized: protocol.NewBlockWithHeight(100)},
		safe:                                  protocol.NewBlockWithHeight(90),
	}
	processor := blocks.NewEthLikeBlockProcessor(context.Background(), upConfig.Id, upConfig.PollInterval, upConfig.Options.InternalTimeout, *upConfig.Options.DisableSafeBlockDetection, mocks.NewConnectorMock(), chainSpecific)
	sub := processor.Subscribe("sub")

	go processor.Start()

	select {
	case event := <-sub.Events:
		assert.Equal(t, protocol.FinalizedBlock, event.BlockType)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for finalized block event")
	}

	select {
	case event := <-sub.Events:
		assert.NotEqual(t, protocol.SafeBlock, event.BlockType)
	case <-time.After(50 * time.Millisecond):
	}
	assert.False(t, processor.DisabledBlocks().Contains(protocol.SafeBlock))
}
