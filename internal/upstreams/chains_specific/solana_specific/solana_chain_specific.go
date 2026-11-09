package solana_specific

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/solana_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/solana_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/solana_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

const checkInterval = 5

type SolanaChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	configuredChain *chains.ConfiguredChain
	options         *chains.Options
	// requestConnector answers the side requests the head path makes. A Solana websocket
	// endpoint serves only the pubsub methods, so a specific built from the ws connector
	// asks getEpochInfo over the upstream's json-rpc connector instead.
	requestConnector connectors.ApiConnector

	lastKnownHeight atomic.Uint64
	lastCheckedSlot atomic.Uint64
}

func (s *SolanaChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return nil
}

func (s *SolanaChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(s.upstreamId, s.connector, solana_labels.NewSolanaClientLabelsDetector(), s.options.InternalTimeout),
	}

	return labels.NewGenericLabelsProcessor(s.ctx, s.upstreamId, labelsDetectors, s.options.ValidationInterval*5)
}

func (s *SolanaChainSpecificObject) CapDetectors(input caps.DetectorInput) []caps.CapDetector {
	return caps.DefaultCapDetectors(s.upstreamId, input.WsConnector)
}

func (s *SolanaChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		solana_bounds.NewSolanaLowerBoundDetector(s.upstreamId, s.options.InternalTimeout, s.connector),
	}
	return lower_bounds.NewGenericLowerBoundProcessor(s.ctx, s.upstreamId, s.configuredChain.AverageRemoveSpeed(), detectors)
}

func (s *SolanaChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	return []validations.Validator[protocol.AvailabilityStatus]{
		solana_validations.NewSolanaHealthValidator(s.upstreamId, s.connector, s.options.InternalTimeout),
	}
}

func (s *SolanaChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	return nil
}

func (s *SolanaChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	return s.getEpochInfo(ctx)
}

func (s *SolanaChainSpecificObject) GetFinalizedBlock(_ context.Context) (protocol.Block, error) {
	// TODO: implement get block/slot with finalized commitment
	return protocol.ZeroBlock{}, nil
}

func (s *SolanaChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	epochInfo := SolanaEpochInfo{}
	err := sonic.Unmarshal(blockBytes, &epochInfo)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the solana block, reason - %s", err.Error())
	}

	return createNewSolanaBlock(epochInfo.BlockHeight, epochInfo.AbsoluteSlot), nil
}

func (s *SolanaChainSpecificObject) ParseSubscriptionBlock(blockBytes []byte) (protocol.Block, error) {
	slotEvent := SolanaSlotEvent{}
	err := sonic.Unmarshal(blockBytes, &slotEvent)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	lastSlot := s.lastCheckedSlot.Load()
	lastHeight := s.lastKnownHeight.Load()
	shouldCheck := slotEvent.Slot >= lastSlot && slotEvent.Slot-lastSlot >= checkInterval
	estimatedHeight := lo.Ternary(lastHeight != 0 && lastSlot != 0, lastHeight+(slotEvent.Slot-lastSlot), 0)

	if shouldCheck || estimatedHeight == 0 {
		block, err := s.getEpochInfo(context.Background())
		if err != nil {
			if estimatedHeight == 0 {
				// a slot is not a height: without a known height there is nothing to publish
				return protocol.ZeroBlock{}, err
			}
			log.Warn().Err(err).Msgf("couldn't get the epoch info for upstream %s, using the estimated height %d, slot %d", s.upstreamId, estimatedHeight, slotEvent.Slot)
			return createNewSolanaBlock(estimatedHeight, slotEvent.Slot), nil
		}
		return createNewSolanaBlock(block.Height, block.Slot), nil
	}
	return createNewSolanaBlock(estimatedHeight, slotEvent.Slot), nil
}

func (s *SolanaChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalSubUpstreamJsonRpcRequest("slotSubscribe", nil, chains.SOLANA)
}

func NewSolanaChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	allConnectors []connectors.ApiConnector,
	options *chains.Options,
) *SolanaChainSpecificObject {
	requestConnector, found := lo.Find(allConnectors, func(c connectors.ApiConnector) bool {
		return c.GetType() == specs.JsonRpcConnector
	})
	if !found {
		requestConnector = connector
	}
	return &SolanaChainSpecificObject{
		ctx:              ctx,
		upstreamId:       upstreamId,
		connector:        connector,
		configuredChain:  configuredChain,
		options:          options,
		requestConnector: requestConnector,
	}
}

func (s *SolanaChainSpecificObject) getEpochInfo(ctx context.Context) (protocol.Block, error) {
	ctx, cancel := context.WithTimeout(ctx, s.options.InternalTimeout)
	defer cancel()

	params := map[string]interface{}{
		"commitment": "confirmed",
	}
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getEpochInfo", []interface{}{params}, chains.SOLANA)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	response := s.requestConnector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}
	block, err := s.ParseBlock(response.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, err
	}

	s.lastKnownHeight.Store(block.Height)
	s.lastCheckedSlot.Store(block.Slot)

	return block, nil
}

func createNewSolanaBlock(height uint64, slot uint64) protocol.Block {
	hash, parentHash := specific_helpers.SyntheticHashes(slot, slot-1)
	return protocol.NewBlock(height, slot, hash, parentHash)
}

type SolanaEpochInfo struct {
	AbsoluteSlot uint64 `json:"absoluteSlot"`
	BlockHeight  uint64 `json:"blockHeight"`
}

type SolanaSlotEvent struct {
	Slot uint64 `json:"slot"`
}

var _ chains_specific.ChainSpecific = (*SolanaChainSpecificObject)(nil)

// MethodsProcessor returns nil: this chain exposes no way to ask a node which methods it
// implements, so its upstreams keep the full method set their spec declares.
func (s *SolanaChainSpecificObject) MethodsProcessor() methods.MethodsProcessor {
	return nil
}

func (s *SolanaChainSpecificObject) PauseHeadWhileSyncing() bool {
	return false
}
