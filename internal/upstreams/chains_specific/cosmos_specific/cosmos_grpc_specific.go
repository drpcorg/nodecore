package cosmos_specific

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
	"github.com/drpcorg/nodecore/internal/upstreams/labels/cosmos_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/cosmos_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/cosmos_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
)

// CosmosGrpcSpecific drives a cosmos upstream through the SDK gRPC API - the
// upstream's only connector. All probes are unary calls on
// cosmos.base.tendermint.v1beta1.Service.
type CosmosGrpcSpecific struct {
	ctx          context.Context
	upstreamId   string
	connector    connectors.ApiConnector
	chain        *chains.ConfiguredChain
	options      *chains.Options
	pollInterval time.Duration
}

func NewCosmosGrpcSpecific(
	ctx context.Context,
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	pollInterval time.Duration,
	options *chains.Options,
) (*CosmosGrpcSpecific, error) {
	if connector == nil {
		return nil, errors.New("no connector specified")
	}
	if connector.GetType() != specs.GrpcConnector {
		return nil, fmt.Errorf("cosmos grpc specific supports only the grpc connector but not %s", connector.GetType())
	}
	return &CosmosGrpcSpecific{
		ctx:          ctx,
		upstreamId:   upstreamId,
		connector:    connector,
		chain:        chain,
		options:      options,
		pollInterval: pollInterval,
	}, nil
}

func (c *CosmosGrpcSpecific) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	raw, err := specific_helpers.FetchCosmosGrpcLatestBlock(ctx, c.connector, c.chain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return c.ParseBlock(raw)
}

// GetFinalizedBlock - a committed cosmos block is final, so the head is also
// the finalized block.
func (c *CosmosGrpcSpecific) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return c.GetLatestBlock(ctx)
}

// ParseBlock expects a serialized GetLatestBlockResponse. The block ids are
// the raw hash bytes the node reports, so they reduce to the same HashId the
// LCD (base64) and the CometBFT RPC (hex) produce for the same block.
func (c *CosmosGrpcSpecific) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	result, err := specific_helpers.ParseCosmosGrpcBlock(blockBytes)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	height, parentHash := specific_helpers.CosmosGrpcBlockHeader(result)
	if height <= 0 {
		return protocol.ZeroBlock{}, fmt.Errorf("cosmos grpc block reports no height")
	}
	return protocol.NewBlock(
		uint64(height),
		0,
		blockchain.NewHashIdFromBytes(result.GetBlockId().GetHash()),
		blockchain.NewHashIdFromBytes(parentHash),
	), nil
}

func (c *CosmosGrpcSpecific) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, blocks.ErrUnsupportedHeadSubscriptions
}

func (c *CosmosGrpcSpecific) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, blocks.ErrUnsupportedHeadSubscriptions
}

func (c *CosmosGrpcSpecific) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	validators := make([]validations.Validator[protocol.AvailabilityStatus], 0, 1)
	if *c.options.ValidateSyncing {
		validators = append(validators, cosmos_validations.NewCosmosGrpcSyncingValidator(
			c.upstreamId, c.chain.Chain, c.connector, c.options.InternalTimeout,
		))
	}
	return validators
}

func (c *CosmosGrpcSpecific) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if c.chain == nil || c.chain.ChainId == "" {
		return nil
	}
	if *c.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		cosmos_validations.NewCosmosGrpcChainValidator(c.upstreamId, c.connector, c.chain, c.options.InternalTimeout),
	}
}

func (c *CosmosGrpcSpecific) CapDetectors(_ caps.DetectorInput) []caps.CapDetector {
	return nil
}

func (c *CosmosGrpcSpecific) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		cosmos_bounds.NewCosmosGrpcLowerBoundDetector(
			c.upstreamId, c.chain.Chain, c.options.InternalTimeout, c.connector,
		),
	}
	return lower_bounds.NewGenericLowerBoundProcessor(c.ctx, c.upstreamId, c.chain.AverageRemoveSpeed(), detectors)
}

func (c *CosmosGrpcSpecific) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			c.upstreamId,
			c.connector,
			cosmos_labels.NewCosmosGrpcClientLabelsDetector(c.chain.Chain),
			c.options.InternalTimeout,
		),
	}
	return labels.NewGenericLabelsProcessor(c.ctx, c.upstreamId, labelsDetectors, c.options.ValidationInterval*5)
}

func (c *CosmosGrpcSpecific) BlockProcessor() blocks.BlockProcessor {
	return blocks.NewGenericBlockProcessor(
		c.ctx,
		c.upstreamId,
		c.pollInterval,
		c.options.InternalTimeout,
		c.options.FinalizedBlockDetectionDisabled(),
		true,
		c.connector,
		c,
	)
}

// MethodsProcessor returns nil: no introspection (the reflection-based
// detection idea stays parked, as for sui).
func (c *CosmosGrpcSpecific) MethodsProcessor() methods.MethodsProcessor {
	return nil
}

var _ chains_specific.ChainSpecific = (*CosmosGrpcSpecific)(nil)
