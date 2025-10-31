package blocks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
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

type BlockProcessor interface {
	utils.Lifecycle
	Subscribe(name string) *utils.Subscription[BlockEvent]
	UpdateBlock(blockData *protocol.BlockData, blockType protocol.BlockType)
	DisabledBlocks() mapset.Set[protocol.BlockType]
}

type BlockEvent struct {
	BlockData *protocol.BlockData
	BlockType protocol.BlockType
}

type EthLikeBlockProcessor struct {
	upConfig         *config.Upstream
	connector        connectors.ApiConnector
	chainSpecific    specific.ChainSpecific
	subManager       *utils.SubscriptionManager[BlockEvent]
	disableDetection mapset.Set[protocol.BlockType]
	manualBlockChan  chan *BlockEvent
	blocks           map[protocol.BlockType]*protocol.BlockData
	lifecycle        *utils.BaseLifecycle
	internalTimeout  time.Duration
}

func (b *EthLikeBlockProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *EthLikeBlockProcessor) Stop() {
	log.Info().Msgf("stopping block processor of upstream '%s'", b.upConfig.Id)
	b.lifecycle.Stop()
}

func NewEthLikeBlockProcessor(
	ctx context.Context,
	upConfig *config.Upstream,
	connector connectors.ApiConnector,
	chainSpecific specific.ChainSpecific,
) *EthLikeBlockProcessor {
	name := fmt.Sprintf("%s_block_processor", upConfig.Id)
	return &EthLikeBlockProcessor{
		upConfig:         upConfig,
		connector:        connector,
		chainSpecific:    chainSpecific,
		disableDetection: mapset.NewSet[protocol.BlockType](),
		manualBlockChan:  make(chan *BlockEvent, 100),
		subManager:       utils.NewSubscriptionManager[BlockEvent](name),
		blocks:           make(map[protocol.BlockType]*protocol.BlockData),
		lifecycle:        utils.NewBaseLifecycle(name, ctx),
		internalTimeout:  upConfig.Options.InternalTimeout,
	}
}

func (b *EthLikeBlockProcessor) UpdateBlock(blockData *protocol.BlockData, blockType protocol.BlockType) {
	b.manualBlockChan <- &BlockEvent{BlockData: blockData, BlockType: blockType}
}

func (b *EthLikeBlockProcessor) Subscribe(name string) *utils.Subscription[BlockEvent] {
	return b.subManager.Subscribe(name)
}

func (b *EthLikeBlockProcessor) DisabledBlocks() mapset.Set[protocol.BlockType] {
	return b.disableDetection
}

func (b *EthLikeBlockProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		b.poll(protocol.FinalizedBlock)
		for {
			select {
			case <-ctx.Done():
				return nil
			case event := <-b.manualBlockChan:
				currentBlock, ok := b.blocks[event.BlockType]
				if !ok || event.BlockData.Height > currentBlock.Height {
					b.subManager.Publish(*event)
				}
			case <-time.After(b.upConfig.PollInterval):
				b.poll(protocol.FinalizedBlock)
			}
		}
	})
}

func (b *EthLikeBlockProcessor) poll(blockType protocol.BlockType) {
	if !b.disableDetection.Contains(blockType) {
		ctx, cancel := context.WithTimeout(b.lifecycle.GetParentContext(), b.internalTimeout)
		defer cancel()

		block, err := b.chainSpecific.GetFinalizedBlock(ctx, b.connector)
		if err != nil {
			var respErr *protocol.ResponseError
			if errors.As(err, &respErr) {
				errStr := err.Error()
				for _, errToDisable := range ethErrorsToDisable {
					if strings.Contains(errStr, errToDisable) {
						b.disableDetection.Add(blockType)
					}
				}
			}
			log.Warn().Err(err).Msgf("couldn't detect finalized block of upstream %s", b.upConfig.Id)
		} else {
			b.blocks[blockType] = block.BlockData
			b.subManager.Publish(BlockEvent{BlockData: block.BlockData, BlockType: blockType})
		}
	}
}

var _ BlockProcessor = (*EthLikeBlockProcessor)(nil)
