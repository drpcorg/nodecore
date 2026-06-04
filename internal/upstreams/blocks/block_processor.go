package blocks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

var ethErrorsToDisable = []string{
	"bad request",
	"block not found",
	"Unknown block",
	"tag not supported on pre-merge network",
	"hex string without 0x prefix",
	"Invalid params",
	"invalid syntax",
	"invalid block number",
}

type safeBlockProvider interface {
	GetSafeBlock(context.Context) (protocol.Block, error)
}

type BlockProcessor interface {
	utils.Lifecycle
	Subscribe(name string) *utils.Subscription[BlockEvent]
	UpdateBlock(blockData protocol.Block, blockType protocol.BlockType)
}

type BlockEvent struct {
	Block     protocol.Block
	BlockType protocol.BlockType
}

type EthLikeBlockProcessor struct {
	upstreamId                string
	connector                 connectors.ApiConnector
	chainSpecific             BlockChainSpecific
	subManager                *utils.SubscriptionManager[BlockEvent]
	disableDetection          atomic.Uint32
	disableSafeBlockDetection bool
	manualBlockChan           chan *BlockEvent
	blocks                    map[protocol.BlockType]protocol.Block
	lifecycle                 *utils.BaseLifecycle
	internalTimeout           time.Duration
	pollInterval              time.Duration
}

func (b *EthLikeBlockProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *EthLikeBlockProcessor) Stop() {
	log.Info().Msgf("stopping block processor of upstream '%s'", b.upstreamId)
	b.lifecycle.Stop()
}

func NewEthLikeBlockProcessor(
	ctx context.Context,
	upstreamId string,
	pollInterval, internalTimeout time.Duration,
	disableSafeBlockDetection bool,
	connector connectors.ApiConnector,
	chainSpecific BlockChainSpecific,
) *EthLikeBlockProcessor {
	name := fmt.Sprintf("%s_block_processor", upstreamId)
	return &EthLikeBlockProcessor{
		upstreamId:                upstreamId,
		connector:                 connector,
		chainSpecific:             chainSpecific,
		disableSafeBlockDetection: disableSafeBlockDetection,
		manualBlockChan:           make(chan *BlockEvent, 100),
		subManager:                utils.NewSubscriptionManager[BlockEvent](name),
		blocks:                    make(map[protocol.BlockType]protocol.Block),
		lifecycle:                 utils.NewBaseLifecycle(name, ctx),
		internalTimeout:           internalTimeout,
		pollInterval:              pollInterval,
	}
}

func (b *EthLikeBlockProcessor) UpdateBlock(blockData protocol.Block, blockType protocol.BlockType) {
	b.manualBlockChan <- &BlockEvent{Block: blockData, BlockType: blockType}
}

func (b *EthLikeBlockProcessor) Subscribe(name string) *utils.Subscription[BlockEvent] {
	return b.subManager.Subscribe(name)
}

func (b *EthLikeBlockProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		go b.pollLoop(ctx, protocol.FinalizedBlock)
		if !b.disableSafeBlockDetection {
			go b.pollLoop(ctx, protocol.SafeBlock)
		}
		go b.blockEventLoop(ctx)
		return nil
	})
}

func (b *EthLikeBlockProcessor) pollLoop(ctx context.Context, blockType protocol.BlockType) {
	b.poll(blockType)

	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.poll(blockType)
		}
	}
}

func (b *EthLikeBlockProcessor) blockEventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-b.manualBlockChan:
			currentBlock, ok := b.blocks[event.BlockType]
			if !ok || event.Block.Height > currentBlock.Height {
				b.blocks[event.BlockType] = event.Block
				b.subManager.Publish(*event)
			}
		}
	}
}

func (b *EthLikeBlockProcessor) poll(blockType protocol.BlockType) {
	if b.detectionDisabled(blockType) {
		return
	}

	ctx, cancel := context.WithTimeout(b.lifecycle.GetParentContext(), b.internalTimeout)
	defer cancel()

	block, err := b.detectBlock(ctx, blockType)
	if err != nil {
		var respErr *protocol.ResponseError
		if errors.As(err, &respErr) {
			errStr := err.Error()
			for _, errToDisable := range ethErrorsToDisable {
				if strings.Contains(errStr, errToDisable) {
					b.disableBlockDetection(blockType)
				}
			}
		}
		log.Error().Err(err).Msgf("couldn't detect %s block of upstream %s", blockType.String(), b.upstreamId)
	} else {
		b.manualBlockChan <- &BlockEvent{Block: block, BlockType: blockType}
	}
}

func (b *EthLikeBlockProcessor) detectionDisabled(blockType protocol.BlockType) bool {
	return b.disableDetection.Load()&blockTypeMask(blockType) != 0
}

func (b *EthLikeBlockProcessor) disableBlockDetection(blockType protocol.BlockType) {
	b.disableDetection.Or(blockTypeMask(blockType))
}

func blockTypeMask(blockType protocol.BlockType) uint32 {
	return 1 << uint(blockType)
}

func (b *EthLikeBlockProcessor) detectBlock(ctx context.Context, blockType protocol.BlockType) (protocol.Block, error) {
	switch blockType {
	case protocol.FinalizedBlock:
		return b.chainSpecific.GetFinalizedBlock(ctx)
	case protocol.SafeBlock:
		safeProvider, ok := b.chainSpecific.(safeBlockProvider)
		if !ok {
			b.disableBlockDetection(protocol.SafeBlock)
			return protocol.ZeroBlock{}, fmt.Errorf("safe block detection is not supported by chain specific %T", b.chainSpecific)
		}
		return safeProvider.GetSafeBlock(ctx)
	default:
		return protocol.ZeroBlock{}, fmt.Errorf("unsupported block type %d", blockType)
	}
}

var _ BlockProcessor = (*EthLikeBlockProcessor)(nil)
