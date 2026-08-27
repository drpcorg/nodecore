package cosmos_specific

import (
	"context"
	"errors"
	"fmt"
	"time"

	specs "github.com/drpcorg/method-specs/pkg/methods"
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
)

type CosmosRestSpecific struct {
	ctx          context.Context
	upstreamId   string
	connector    connectors.ApiConnector
	chain        *chains.ConfiguredChain
	options      *chains.Options
	pollInterval time.Duration
}

func newCosmosRestSpecific(
	ctx context.Context,
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	pollInterval time.Duration,
	options *chains.Options,
) (*CosmosRestSpecific, error) {
	if connector == nil {
		return nil, errors.New("no connector specified")
	}
	if connector.GetType() != specs.RestConnector {
		return nil, fmt.Errorf("cosmos rest specific supports only the rest connector but not %s", connector.GetType())
	}
	return &CosmosRestSpecific{
		ctx:          ctx,
		upstreamId:   upstreamId,
		connector:    connector,
		chain:        chain,
		options:      options,
		pollInterval: pollInterval,
	}, nil
}

func (c *CosmosRestSpecific) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	raw, err := specific_helpers.FetchCosmosLatestBlock(ctx, c.connector, c.chain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return c.ParseBlock(raw)
}

// GetFinalizedBlock - a committed cosmos block is final, so the head is also
// the finalized block.
func (c *CosmosRestSpecific) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return c.GetLatestBlock(ctx)
}

func (c *CosmosRestSpecific) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	result, err := specific_helpers.ParseCosmosBlock(blockBytes)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the cosmos block, reason - %s", err.Error())
	}
	height, err := specific_helpers.ParseDecimalHeight(result.Block.Header.Height)
	if err != nil || height == 0 {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the cosmos block, got '%s'", string(blockBytes))
	}
	return protocol.NewBlock(
		height,
		0,
		blockchain.NewHashIdFromString(result.BlockId.Hash),
		blockchain.NewHashIdFromString(result.Block.Header.LastBlockId.Hash),
	), nil
}

func (c *CosmosRestSpecific) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, blocks.ErrUnsupportedHeadSubscriptions
}

func (c *CosmosRestSpecific) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, blocks.ErrUnsupportedHeadSubscriptions
}

func (c *CosmosRestSpecific) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	validators := make([]validations.Validator[protocol.AvailabilityStatus], 0, 1)
	if *c.options.ValidateSyncing {
		validators = append(validators, cosmos_validations.NewCosmosSyncingValidator(
			c.upstreamId, c.chain.Chain, c.connector, c.options.InternalTimeout,
		))
	}
	return validators
}

func (c *CosmosRestSpecific) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if c.chain == nil || c.chain.ChainId == "" {
		return nil
	}
	if *c.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		cosmos_validations.NewCosmosChainValidator(c.upstreamId, c.connector, c.chain, c.options.InternalTimeout),
	}
}

func (c *CosmosRestSpecific) CapDetectors(_ caps.DetectorInput) []caps.CapDetector {
	return nil
}

func (c *CosmosRestSpecific) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		cosmos_bounds.NewCosmosLowerBoundDetector(
			c.upstreamId, c.chain.Chain, c.options.InternalTimeout, c.connector,
		),
	}
	return lower_bounds.NewGenericLowerBoundProcessor(c.ctx, c.upstreamId, c.chain.AverageRemoveSpeed(), detectors)
}

func (c *CosmosRestSpecific) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			c.upstreamId,
			c.connector,
			cosmos_labels.NewCosmosClientLabelsDetector(c.chain.Chain),
			c.options.InternalTimeout,
		),
	}
	return labels.NewGenericLabelsProcessor(c.ctx, c.upstreamId, labelsDetectors, c.options.ValidationInterval*5)
}

func (c *CosmosRestSpecific) BlockProcessor() blocks.BlockProcessor {
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

var _ chains_specific.ChainSpecific = (*CosmosRestSpecific)(nil)

// MethodsProcessor returns nil: this chain exposes no way to ask a node which methods it
// implements, so its upstreams keep the full method set their spec declares.
func (c *CosmosRestSpecific) MethodsProcessor() methods.MethodsProcessor {
	return nil
}
