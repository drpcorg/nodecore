package tendermint_specific

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/tendermint_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/tendermint_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/tendermint_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

var errUnsupportedHeadSubscriptions = errors.New("tendermint: head subscriptions are not supported")

type TendermintChainSpecific struct {
	ctx          context.Context
	upstreamId   string
	connector    connectors.ApiConnector
	chain        *chains.ConfiguredChain
	options      *chains.Options
	pollInterval time.Duration
}

func NewTendermintSpecific(
	ctx context.Context,
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	pollInterval time.Duration,
	options *chains.Options,
) (*TendermintChainSpecific, error) {
	if connector == nil {
		return nil, errors.New("no connector specified")
	}
	if connector.GetType() != specs.TendermintConnector {
		return nil, fmt.Errorf("tendermint specific supports only the tendermint connector but not %s", connector.GetType())
	}
	return &TendermintChainSpecific{
		ctx:          ctx,
		upstreamId:   upstreamId,
		connector:    connector,
		chain:        chain,
		options:      options,
		pollInterval: pollInterval,
	}, nil
}

func (t *TendermintChainSpecific) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	raw, err := specific_helpers.TendermintCall(
		ctx, t.connector, t.chain.Chain, "block", nil,
	)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return t.ParseBlock(raw)
}

// GetFinalizedBlock - CometBFT commits are final the moment they are produced
// (+2/3 pre-commits), so the latest block is also the finalized one.
func (t *TendermintChainSpecific) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return t.GetLatestBlock(ctx)
}

func (t *TendermintChainSpecific) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	var result TendermintBlockResult
	if err := sonic.Unmarshal(blockBytes, &result); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the tendermint block, reason - %s", err.Error())
	}
	height, err := specific_helpers.ParseDecimalHeight(result.Block.Header.Height)
	if err != nil || height == 0 {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the tendermint block, got '%s'", string(blockBytes))
	}
	return protocol.NewBlock(
		height,
		0,
		blockchain.NewHashIdFromString(result.BlockId.Hash),
		blockchain.NewHashIdFromString(result.Block.Header.LastBlockId.Hash),
	), nil
}

func (t *TendermintChainSpecific) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedHeadSubscriptions
}

func (t *TendermintChainSpecific) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, errUnsupportedHeadSubscriptions
}

func (t *TendermintChainSpecific) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	validators := make([]validations.Validator[protocol.AvailabilityStatus], 0, 2)
	if *t.options.ValidateSyncing {
		validators = append(validators, tendermint_validations.NewTendermintSyncingValidator(
			t.upstreamId, t.chain.Chain, t.connector, t.options.InternalTimeout,
		))
	}
	if *t.options.ValidatePeers {
		validators = append(validators, tendermint_validations.NewTendermintPeersValidator(
			t.upstreamId, t.chain.Chain, t.connector, t.options,
		))
	}
	return validators
}

func (t *TendermintChainSpecific) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if t.chain == nil || t.chain.ChainId == "" {
		return nil
	}
	if *t.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		tendermint_validations.NewTendermintChainValidator(t.upstreamId, t.connector, t.chain, t.options.InternalTimeout),
	}
}

func (t *TendermintChainSpecific) CapDetectors(_ caps.DetectorInput) []caps.CapDetector {
	return nil
}

func (t *TendermintChainSpecific) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		tendermint_bounds.NewTendermintLowerBoundDetector(
			t.upstreamId, t.chain.Chain, t.options.InternalTimeout, t.connector,
		),
	}
	return lower_bounds.NewGenericLowerBoundProcessor(t.ctx, t.upstreamId, t.chain.AverageRemoveSpeed(), detectors)
}

func (t *TendermintChainSpecific) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			t.upstreamId,
			t.connector,
			tendermint_labels.NewTendermintClientLabelsDetector(t.chain.Chain),
			t.options.InternalTimeout,
		),
	}
	return labels.NewGenericLabelsProcessor(t.ctx, t.upstreamId, labelsDetectors, t.options.ValidationInterval*5)
}

func (t *TendermintChainSpecific) BlockProcessor() blocks.BlockProcessor {
	return blocks.NewGenericBlockProcessor(
		t.ctx,
		t.upstreamId,
		t.pollInterval,
		t.options.InternalTimeout,
		t.options.FinalizedBlockDetectionDisabled(),
		true,
		t.connector,
		t,
	)
}

type TendermintBlockResult struct {
	BlockId TendermintBlockId   `json:"block_id"`
	Block   TendermintBlockData `json:"block"`
}

type TendermintBlockId struct {
	Hash string `json:"hash"`
}

type TendermintBlockData struct {
	Header TendermintHeader `json:"header"`
}

type TendermintHeader struct {
	Height      string            `json:"height"`
	Time        string            `json:"time"`
	LastBlockId TendermintBlockId `json:"last_block_id"`
}

var _ chains_specific.ChainSpecific = (*TendermintChainSpecific)(nil)

// MethodsProcessor returns nil: this chain exposes no way to ask a node which methods it
// implements, so its upstreams keep the full method set their spec declares.
func (t *TendermintChainSpecific) MethodsProcessor() methods.MethodsProcessor {
	return nil
}
