package starknet_specific

import (
	"context"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/starknet_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/starknet_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/starknet_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// errUnsupportedHeadSubscriptions is returned by SubscribeHeadRequest and
// ParseSubscriptionBlock: spec-v0.8 starknet subscriptions are WS-only and
// out of v1 scope, so head tracking is poll-only.
var errUnsupportedHeadSubscriptions = fmt.Errorf("starknet: head subscriptions are not supported")

type StarknetChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	pollInterval    time.Duration
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewStarknetChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	pollInterval time.Duration,
	options *chains.Options,
) *StarknetChainSpecificObject {
	return &StarknetChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		pollInterval:    pollInterval,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		configuredChain: configuredChain,
	}
}

func (s *StarknetChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return blocks.NewBaseBlockProcessor(
		s.ctx,
		s.upstreamId,
		s.pollInterval,
		s.internalTimeout,
		s.options.FinalizedBlockDetectionDisabled(),
		true, // starknet has no "safe" block concept, only l2/l1 acceptance
		s.connector,
		s,
	)
}

func (s *StarknetChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		starknet_labels.NewStarknetLabelsDetector(
			s.upstreamId,
			s.connector,
			s.configuredChain.Chain,
			s.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(s.ctx, s.upstreamId, labelsDetectors, s.labelsDelay)
}

func (s *StarknetChainSpecificObject) CapDetectors(_ caps.DetectorInput) []caps.CapDetector {
	// no ws transport in v1 scope, so no ws-derived caps can ever be asserted
	return nil
}

func (s *StarknetChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		starknet_bounds.NewStarknetLowerBoundDetector(s.upstreamId),
	}
	return lower_bounds.NewBaseLowerBoundProcessor(
		s.ctx,
		s.upstreamId,
		s.configuredChain.AverageRemoveSpeed(),
		detectors,
	)
}

func (s *StarknetChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if s.options != nil && *s.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	validators := make([]validations.Validator[protocol.AvailabilityStatus], 0)
	if s.options != nil && s.options.ValidateSyncing != nil && *s.options.ValidateSyncing {
		validators = append(validators, starknet_validations.NewStarknetSyncingValidator(
			s.upstreamId, s.connector, s.configuredChain, s.internalTimeout,
		))
	}
	return validators
}

func (s *StarknetChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if s.configuredChain == nil || s.configuredChain.ChainId == "" {
		return nil
	}
	if s.options != nil && *s.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		starknet_validations.NewStarknetChainValidator(s.upstreamId, s.connector, s.configuredChain, s.internalTimeout),
	}
}

// GetLatestBlock polls starknet_getBlockWithTxHashes("latest") - the newest
// ACCEPTED_ON_L2 block, same source dshackle used. blockHashAndNumber would
// be cheaper but lacks parent_hash.
func (s *StarknetChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	return s.fetchBlockByTag(ctx, "latest")
}

// GetFinalizedBlock polls the "l1_accepted" tag (RPC spec >= 0.9): the newest
// block already ACCEPTED_ON_L1, which lags "latest" by hours. On pre-0.9
// upstreams the tag errors out and the block processor disables finalized
// detection for them.
func (s *StarknetChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return s.fetchBlockByTag(ctx, "l1_accepted")
}

func (s *StarknetChainSpecificObject) fetchBlockByTag(ctx context.Context, tag string) (protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"starknet_getBlockWithTxHashes",
		[]any{tag},
		s.configuredChain.Chain,
	)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	block, err := s.ParseBlock(response.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the starknet %s block: %w", tag, err)
	}
	return block, nil
}

// ParseBlock expects the payload shape of starknet_getBlockWithTxHashes:
// {"block_hash", "block_number", "parent_hash", "timestamp", ...}.
func (s *StarknetChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	header := starknetBlockHeader{}
	if err := sonic.Unmarshal(blockBytes, &header); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the starknet block, reason - %s", err.Error())
	}
	if header.BlockHash == "" {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the starknet block, got '%s'", string(blockBytes))
	}

	parentHash := blockchain.EmptyHash
	if header.ParentHash != "" {
		parentHash = blockchain.NewHashIdFromString(header.ParentHash)
	}

	return protocol.NewBlock(
		header.BlockNumber,
		0,
		blockchain.NewHashIdFromString(header.BlockHash),
		parentHash,
	), nil
}

func (s *StarknetChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedHeadSubscriptions
}

func (s *StarknetChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, errUnsupportedHeadSubscriptions
}

type starknetBlockHeader struct {
	BlockHash   string `json:"block_hash"`
	BlockNumber uint64 `json:"block_number"`
	ParentHash  string `json:"parent_hash"`
}

var _ chains_specific.ChainSpecific = (*StarknetChainSpecificObject)(nil)
