package blocks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/protocol"
	specific "github.com/drpcorg/dsheltie/internal/upstreams/chains_specific"
	"github.com/drpcorg/dsheltie/internal/upstreams/connectors"
	"github.com/drpcorg/dsheltie/pkg/utils"
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
	Start()
	Subscribe(name string) *utils.Subscription[BlockEvent]
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
	ctx              context.Context
	disableDetection mapset.Set[protocol.BlockType]
}

func NewEthLikeBlockProcessor(
	ctx context.Context,
	upConfig *config.Upstream,
	connector connectors.ApiConnector,
	chainSpecific specific.ChainSpecific,
) *EthLikeBlockProcessor {
	return &EthLikeBlockProcessor{
		ctx:              ctx,
		upConfig:         upConfig,
		connector:        connector,
		chainSpecific:    chainSpecific,
		disableDetection: mapset.NewSet[protocol.BlockType](),
		subManager:       utils.NewSubscriptionManager[BlockEvent](fmt.Sprintf("%s_block_processor", upConfig.Id)),
	}
}

func (b *EthLikeBlockProcessor) Subscribe(name string) *utils.Subscription[BlockEvent] {
	return b.subManager.Subscribe(name)
}

func (b *EthLikeBlockProcessor) DisabledBlocks() mapset.Set[protocol.BlockType] {
	return b.disableDetection
}

func (b *EthLikeBlockProcessor) Start() {
	for {
		b.poll(protocol.FinalizedBlock)
		select {
		case <-b.ctx.Done():
			return
		case <-time.After(b.upConfig.PollInterval):
		}
	}
}

func (b *EthLikeBlockProcessor) poll(blockType protocol.BlockType) {
	if !b.disableDetection.Contains(blockType) {
		ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
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
			b.subManager.Publish(BlockEvent{BlockData: block.BlockData, BlockType: blockType})
		}
	}
}

var _ BlockProcessor = (*EthLikeBlockProcessor)(nil)
