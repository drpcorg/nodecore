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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var errSuiNoCheckpointHeight = errors.New("sui node reported no checkpoint_height")

var errSuiNoCheckpointCursor = errors.New("sui SubscribeCheckpoints frame carries no cursor")

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

// ParseSubscriptionBlock reads a SubscribeCheckpoints frame. The height is the
// cursor - present on every frame, monotonic, and on an unfiltered stream equal
// to the delivered checkpoint's sequence number - so the checkpoint payload is
// never inspected and progress-only frames (filtered streams only) need no
// special handling. Hashes stay synthetic, matching the polled head.
func (s *SuiChainSpecificObject) ParseSubscriptionBlock(data []byte) (protocol.Block, error) {
	var frame sui.SubscribeCheckpointsResponse
	if err := proto.Unmarshal(data, &frame); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the sui SubscribeCheckpoints frame, reason - %s", err.Error())
	}
	if frame.Cursor == nil {
		return protocol.ZeroBlock{}, errSuiNoCheckpointCursor
	}
	return newSuiBlockFromHeight(frame.GetCursor(), data)
}

// SubscribeHeadRequest opens an unfiltered SubscribeCheckpoints stream with the
// read mask cut down to the sequence number - the cursor is what the head
// reads, the mask only keeps frames small.
func (s *SuiChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	body, err := proto.Marshal(&sui.SubscribeCheckpointsRequest{
		ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"sequence_number"}},
	})
	if err != nil {
		return nil, fmt.Errorf("couldn't marshal the sui SubscribeCheckpoints request: %w", err)
	}
	return protocol.NewInternalUpstreamGrpcRequest("/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints", body, s.configuredChain.Chain), nil
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

// CapDetectors returns nil: Sui has no EVM-style local-synthesis caps.
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

// BlockProcessor is the standard block processor (the head itself is chosen
// by head-mode, see blocks.createHead). Checkpoints are final on execution, so
// there is no separate "safe" pointer - safe block detection is disabled
// unconditionally.
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

func newSuiBlock(serviceInfo *sui.GetServiceInfoResponse, rawData []byte) (protocol.Block, error) {
	return newSuiBlockFromHeight(serviceInfo.GetCheckpointHeight(), rawData)
}

// newSuiBlockFromHeight builds a head block from a checkpoint height. Neither
// GetServiceInfo nor the cursor exposes a digest, so the hashes are synthetic -
// deterministic height encodings that keep block(N).ParentHash == block(N-1).Hash,
// so the parent-linkage checks in head-stream consumers hold. Checkpoints are
// BFT-final on execution (no reorgs), so height-derived ids are safe.
func newSuiBlockFromHeight(height uint64, rawData []byte) (protocol.Block, error) {
	if height == 0 {
		return protocol.ZeroBlock{}, errSuiNoCheckpointHeight
	}
	hash, parentHash := specific_helpers.SyntheticHashes(height, height-1)
	block := protocol.NewBlock(height, 0, hash, parentHash)
	block.RawData = rawData
	return block, nil
}

var _ chains_specific.ChainSpecific = (*SuiChainSpecificObject)(nil)
