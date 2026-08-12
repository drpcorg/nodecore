package polkadot_specific

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
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/polkadot_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/polkadot_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errUnsupportedFinalizedBlock = errors.New("polkadot: finalized block detection is not supported")

type PolkadotChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	internalTimeout time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewPolkadotChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	options *chains.Options,
) *PolkadotChainSpecificObject {
	return &PolkadotChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		internalTimeout: options.InternalTimeout,
		configuredChain: configuredChain,
	}
}

func (p *PolkadotChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	header, err := specific_helpers.FetchPolkadotHeader(ctx, p.connector, p.configuredChain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	block, err := blockFromHeader(header)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	hash, err := specific_helpers.FetchPolkadotBlockHash(ctx, p.connector, p.configuredChain.Chain, header.Number)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf(
			"couldn't resolve the hash of polkadot block %s: %w", header.Number, err,
		)
	}
	block.Hash = blockchain.NewHashIdFromString(hash)
	return block, nil
}

func (p *PolkadotChainSpecificObject) GetFinalizedBlock(_ context.Context) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedFinalizedBlock
}

func (p *PolkadotChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	header, err := specific_helpers.ParsePolkadotHeader(blockBytes)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return blockFromHeader(header)
}

func (p *PolkadotChainSpecificObject) ParseSubscriptionBlock(blockBytes []byte) (protocol.Block, error) {
	header, err := specific_helpers.ParsePolkadotHeader(blockBytes)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	block, err := blockFromHeader(header)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.internalTimeout)
	defer cancel()

	hash, err := specific_helpers.FetchPolkadotBlockHash(ctx, p.connector, p.configuredChain.Chain, header.Number)
	if err != nil {
		log.Warn().Err(err).Msgf(
			"couldn't resolve the hash of polkadot block %s of upstream '%s', publishing the head without it",
			header.Number, p.upstreamId,
		)
		return block, nil
	}
	block.Hash = blockchain.NewHashIdFromString(hash)
	return block, nil
}

func (p *PolkadotChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalSubUpstreamJsonRpcRequest(
		"chain_subscribeNewHeads", []any{}, p.configuredChain.Chain,
	)
}

func (p *PolkadotChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if *p.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	validateSyncing := *p.options.ValidateSyncing
	validatePeers := *p.options.ValidatePeers
	// Both arms come from one system_health response, so with both checks off
	// there is nothing to learn and no reason to spend the call.
	if !validateSyncing && !validatePeers {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{
		polkadot_validations.NewPolkadotHealthValidator(
			p.upstreamId,
			p.connector,
			p.configuredChain.Chain,
			p.internalTimeout,
			validateSyncing,
			validatePeers,
			p.options.MinPeers,
		),
	}
}

func (p *PolkadotChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if p.configuredChain.ChainId == "" {
		return nil
	}
	if *p.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		polkadot_validations.NewPolkadotChainValidator(
			p.upstreamId, p.connector, p.configuredChain, p.internalTimeout,
		),
	}
}

func (p *PolkadotChainSpecificObject) CapDetectors(input caps.DetectorInput) []caps.CapDetector {
	return caps.DefaultCapDetectors(p.upstreamId, input.WsConnector)
}

func (p *PolkadotChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		polkadot_bounds.NewPolkadotLowerBoundDetector(
			p.upstreamId, p.configuredChain.Chain, p.internalTimeout, p.connector,
		),
	}
	return lower_bounds.NewGenericLowerBoundProcessor(
		p.ctx, p.upstreamId, p.configuredChain.AverageRemoveSpeed(), detectors,
	)
}

func (p *PolkadotChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	return nil
}

func (p *PolkadotChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return nil
}

func blockFromHeader(header *specific_helpers.PolkadotHeader) (protocol.Block, error) {
	height, err := specific_helpers.ParsePolkadotHeight(header.Number)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	parentHash := blockchain.EmptyHash
	if header.ParentHash != "" {
		parentHash = blockchain.NewHashIdFromString(header.ParentHash)
	}
	return protocol.NewBlock(height, 0, blockchain.EmptyHash, parentHash), nil
}

var _ chains_specific.ChainSpecific = (*PolkadotChainSpecificObject)(nil)
