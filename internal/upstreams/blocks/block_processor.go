package blocks

import (
	"context"
	"errors"
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dshaltie/internal/config"
	"github.com/drpcorg/dshaltie/internal/protocol"
	specific "github.com/drpcorg/dshaltie/internal/upstreams/chains_specific"
	"github.com/drpcorg/dshaltie/internal/upstreams/connectors"
	"github.com/drpcorg/dshaltie/pkg/utils"
	"github.com/rs/zerolog/log"
	"strings"
	"time"
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
			if errors.Is(err, &protocol.ResponseError{}) {
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
