package event_processors

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

var blocksMetric = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "blocks",
		Help:      "The current block height of a specific block type",
	},
	[]string{"upstream", "blockType", "chain"},
)

var headsMetric = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "heads",
		Help:      "The current head height",
	},
	[]string{"chain", "upstream"},
)

func init() {
	prometheus.MustRegister(blocksMetric, headsMetric)
}

type BlockUpdateData interface {
	data()
}

type HeadUpdateData struct {
	height uint64
	slot   uint64
}

func (u *HeadUpdateData) data() {}

func NewHeadUpdateData(height, slot uint64) *HeadUpdateData {
	return &HeadUpdateData{
		height: height,
		slot:   slot,
	}
}

type BaseBlockUpdateData struct {
	block     protocol.Block
	blockType protocol.BlockType
}

func (b *BaseBlockUpdateData) data() {}

func NewBaseBlockUpdateData(block protocol.Block, blockType protocol.BlockType) *BaseBlockUpdateData {
	return &BaseBlockUpdateData{
		block:     block,
		blockType: blockType,
	}
}

type BlockEventProcessor interface {
	UpstreamStateEventProcessor

	UpdateBlock(data BlockUpdateData)
}

type HeadEventProcessor struct {
	upstreamId    string
	chain         chains.Chain
	lifecycle     *utils.BaseLifecycle
	headProcessor blocks.HeadProcessor
	emitter       Emitter
}

func (h *HeadEventProcessor) SetEmitter(emitter Emitter) {
	h.emitter = emitter
}

func (h *HeadEventProcessor) Type() EventProcessorType {
	return HeadEventProcessorType
}

func (h *HeadEventProcessor) Start() {
	h.lifecycle.Start(func(ctx context.Context) error {
		h.headProcessor.Start()

		headSub := h.headProcessor.Subscribe(fmt.Sprintf("%s_head_updates", h.upstreamId))

		go func() {
			defer headSub.Unsubscribe()
			for {
				select {
				case <-ctx.Done():
					log.Info().Msgf("stopping head events of upstream '%s'", h.upstreamId)
					return
				case head, ok := <-headSub.Events:
					if ok {
						h.emitter(&protocol.HeadUpstreamStateEvent{HeadData: head.HeadData})
						headsMetric.WithLabelValues(h.chain.String(), h.upstreamId).Set(float64(head.HeadData.Height))
					}
				}
			}
		}()

		return nil
	})
}

func (h *HeadEventProcessor) Stop() {
	h.lifecycle.Stop()
	h.headProcessor.Stop()
}

func (h *HeadEventProcessor) Running() bool {
	return h.lifecycle.Running()
}

func (h *HeadEventProcessor) UpdateBlock(data BlockUpdateData) {
	if headData, ok := data.(*HeadUpdateData); ok {
		h.headProcessor.UpdateHead(headData.height, headData.slot)
	} else {
		log.Warn().Msgf("HeadEventProcessor got unsupported BlockUpdateData type: %T", data)
	}
}

func NewHeadEventProcessor(
	ctx context.Context,
	upstreamId string,
	chain chains.Chain,
	headProcessor blocks.HeadProcessor,
) *HeadEventProcessor {
	if headProcessor == nil {
		return nil
	}

	return &HeadEventProcessor{
		upstreamId:    upstreamId,
		chain:         chain,
		lifecycle:     utils.NewBaseLifecycle(fmt.Sprintf("%s_head_event_processor", upstreamId), ctx),
		headProcessor: headProcessor,
	}
}

type BaseBlockEventProcessor struct {
	upstreamId     string
	chain          chains.Chain
	lifecycle      *utils.BaseLifecycle
	blockProcessor blocks.BlockProcessor
	emitter        Emitter
}

func (b *BaseBlockEventProcessor) SetEmitter(emitter Emitter) {
	b.emitter = emitter
}

func (b *BaseBlockEventProcessor) Type() EventProcessorType {
	return BlockEventProcessorType
}

func (b *BaseBlockEventProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		b.blockProcessor.Start()

		blockSub := b.blockProcessor.Subscribe(fmt.Sprintf("%s_block_updates", b.upstreamId))

		go func() {
			defer blockSub.Unsubscribe()
			for {
				select {
				case <-ctx.Done():
					log.Info().Msgf("stopping block events of upstream '%s'", b.upstreamId)
					return
				case block, ok := <-blockSub.Events:
					if ok {
						b.emitter(&protocol.BlockUpstreamStateEvent{Block: block.Block, BlockType: block.BlockType})
						blocksMetric.WithLabelValues(b.upstreamId, block.BlockType.String(), b.chain.String()).Set(float64(block.Block.Height))
					}
				}
			}
		}()

		return nil
	})
}

func (b *BaseBlockEventProcessor) Stop() {
	b.lifecycle.Stop()
	b.blockProcessor.Stop()
}

func (b *BaseBlockEventProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *BaseBlockEventProcessor) UpdateBlock(data BlockUpdateData) {
	if blockUpdateData, ok := data.(*BaseBlockUpdateData); ok {
		b.blockProcessor.UpdateBlock(blockUpdateData.block, blockUpdateData.blockType)
	} else {
		log.Warn().Msgf("BaseBlockEventProcessor got unsupported BlockUpdateData type: %T", data)
	}
}

func NewBaseBlockEventProcessor(
	ctx context.Context,
	upstreamId string,
	chain chains.Chain,
	blockProcessor blocks.BlockProcessor,
) *BaseBlockEventProcessor {
	if blockProcessor == nil {
		return nil
	}

	return &BaseBlockEventProcessor{
		lifecycle:      utils.NewBaseLifecycle(fmt.Sprintf("%s_block_event_processor", upstreamId), ctx),
		upstreamId:     upstreamId,
		chain:          chain,
		blockProcessor: blockProcessor,
	}
}

var _ BlockEventProcessor = (*BaseBlockEventProcessor)(nil)
var _ BlockEventProcessor = (*HeadEventProcessor)(nil)
