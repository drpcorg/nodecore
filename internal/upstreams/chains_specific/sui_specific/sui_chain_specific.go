package sui_specific

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/sui_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/sui_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/sui_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/sui"
)

// errUnsupportedHeadSubscriptions - v1 has no head subscription (pushing
// heads via SubscribeCheckpoints belongs to the server-streaming follow-up),
// so head tracking is poll-only, the pattern Stellar/Aptos use today.
var errUnsupportedHeadSubscriptions = errors.New("sui: head subscriptions are not supported")

var errSuiNoCheckpointHeight = errors.New("sui node reported no checkpoint_height")

// SuiChainSpecificObject drives an upstream through the sui.rpc.v2 gRPC API -
// the upstream's only connector. All probes are unary gRPC calls reading the
// single source, LedgerService/GetServiceInfo.
type SuiChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	pollInterval    time.Duration
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewSuiChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	pollInterval time.Duration,
	options *chains.Options,
) *SuiChainSpecificObject {
	return &SuiChainSpecificObject{
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

// GetLatestBlock polls GetServiceInfo. The response carries no checkpoint
// digest, so the block ids are synthetic (see newSuiBlock).
func (s *SuiChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	serviceInfo, rawData, err := specific_helpers.FetchSuiServiceInfo(ctx, s.connector, s.configuredChain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return newSuiBlock(serviceInfo, rawData)
}

// GetFinalizedBlock - an executed checkpoint *is* final; Sui has no separate
// finalized pointer, so the finalized block is the head.
func (s *SuiChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return s.GetLatestBlock(ctx)
}

// ParseBlock expects a serialized GetServiceInfoResponse.
func (s *SuiChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	serviceInfo, err := specific_helpers.ParseSuiServiceInfo(blockBytes)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the sui GetServiceInfo result, reason - %s", err.Error())
	}
	return newSuiBlock(serviceInfo, blockBytes)
}

func (s *SuiChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedHeadSubscriptions
}

func (s *SuiChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, errUnsupportedHeadSubscriptions
}

func (s *SuiChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	return []validations.Validator[protocol.AvailabilityStatus]{
		sui_validations.NewSuiHealthValidator(s.upstreamId, s.connector, s.configuredChain, s.internalTimeout),
	}
}

func (s *SuiChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if s.configuredChain.ChainId == "" {
		return nil
	}
	if *s.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		sui_validations.NewSuiChainValidator(s.upstreamId, s.connector, s.configuredChain, s.internalTimeout),
	}
}

// CapDetectors returns nil: v1 has no streaming transport, so no cap can be
// asserted.
func (s *SuiChainSpecificObject) CapDetectors(_ caps.DetectorInput) []caps.CapDetector {
	return nil
}

func (s *SuiChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		sui_bounds.NewSuiLowerBoundDetector(
			s.upstreamId, s.configuredChain.Chain, s.internalTimeout, s.connector,
		),
	}
	return lower_bounds.NewGenericLowerBoundProcessor(
		s.ctx, s.upstreamId, s.configuredChain.AverageRemoveSpeed(), detectors,
	)
}

func (s *SuiChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			s.upstreamId,
			s.connector,
			sui_labels.NewSuiClientLabelsDetector(s.configuredChain.Chain),
			s.internalTimeout,
		),
	}
	return labels.NewGenericLabelsProcessor(s.ctx, s.upstreamId, labelsDetectors, s.labelsDelay)
}

// BlockProcessor is the standard poll-based head processor. Checkpoints are
// final on execution, so there is no separate "safe" pointer - safe block
// detection is disabled unconditionally.
func (s *SuiChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return blocks.NewGenericBlockProcessor(
		s.ctx,
		s.upstreamId,
		s.pollInterval,
		s.internalTimeout,
		s.options.FinalizedBlockDetectionDisabled(),
		true,
		s.connector,
		s,
	)
}

// MethodsProcessor returns nil: no introspection in v1 (the server-reflection
// idea is parked).
func (s *SuiChainSpecificObject) MethodsProcessor() methods.MethodsProcessor {
	return nil
}

// newSuiBlock builds a head block from the checkpoint height. GetServiceInfo
// exposes no checkpoint digest, so the hashes are synthetic - deterministic
// height encodings that keep block(N).ParentHash == block(N-1).Hash, so the
// parent-linkage checks in head-stream consumers hold. Checkpoints are
// BFT-final on execution (no reorgs), so height-derived ids are safe.
func newSuiBlock(serviceInfo *sui.GetServiceInfoResponse, rawData []byte) (protocol.Block, error) {
	height := serviceInfo.GetCheckpointHeight()
	if height == 0 {
		return protocol.ZeroBlock{}, errSuiNoCheckpointHeight
	}
	hash, parentHash := specific_helpers.SyntheticHashes(height, height-1)
	block := protocol.NewBlock(height, 0, hash, parentHash)
	block.RawData = rawData
	return block, nil
}

var _ chains_specific.ChainSpecific = (*SuiChainSpecificObject)(nil)
