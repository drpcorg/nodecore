package aztec_specific

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
	"github.com/drpcorg/nodecore/internal/upstreams/labels/aztec_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/aztec_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/aztec_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type AztecChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
	options         *chains.Options
	// tips bridges the v5 rename node_getL2Tips -> node_getChainTips and caches
	// the working method for this upstream. It is shared with the health
	// validator so a v5 node is probed for the renamed method only once.
	tips *aztec_validations.TipsMethodResolver
}

func (a *AztecChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return nil
}

func NewAztecChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	options *chains.Options,
	connector connectors.ApiConnector,
) *AztecChainSpecificObject {
	return &AztecChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		options:         options,
		connector:       connector,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		configuredChain: configuredChain,
		tips:            aztec_validations.NewTipsMethodResolver(),
	}
}

func (a *AztecChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			a.upstreamId,
			a.connector,
			aztec_labels.NewAztecClientLabelsDetector(a.configuredChain.Chain),
			a.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(a.ctx, a.upstreamId, labelsDetectors, a.labelsDelay)
}

func (a *AztecChainSpecificObject) CapDetectors(input caps.DetectorInput) []caps.CapDetector {
	return caps.DefaultCapDetectors(a.upstreamId, input.WsConnector)
}

func (a *AztecChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		aztec_bounds.NewAztecLowerBoundDetector(
			a.upstreamId,
			a.configuredChain.Chain,
			a.internalTimeout,
			a.connector,
		),
	}
	return lower_bounds.NewBaseLowerBoundProcessor(
		a.ctx,
		a.upstreamId,
		a.configuredChain.AverageRemoveSpeed(),
		detectors,
	)
}

func (a *AztecChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if a.options != nil && *a.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{
		aztec_validations.NewAztecHealthValidator(
			a.upstreamId, a.connector, a.configuredChain.Chain, a.internalTimeout, a.tips,
		),
	}
}

func (a *AztecChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if a.configuredChain == nil || a.configuredChain.ChainId == "" {
		return nil
	}
	if a.options != nil && *a.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		aztec_validations.NewAztecChainValidator(a.upstreamId, a.connector, a.configuredChain, a.internalTimeout),
	}
}

func (a *AztecChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	response, err := a.tips.FetchTips(ctx, a.connector, a.configuredChain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	return a.ParseBlock(response.ResponseResult())
}

func (a *AztecChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	response, err := a.tips.FetchTips(ctx, a.connector, a.configuredChain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	tips := aztec_validations.AztecL2Tips{}
	if err := sonic.Unmarshal(response.ResponseResult(), &tips); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf(
			"couldn't parse aztec L2 tips, reason - %s", err.Error(),
		)
	}
	// Aztec exposes both `proven` (zk proof submitted) and `finalized` (the L1
	// block carrying that proof is finalized). Finalized is the stronger notion
	// expected by GetFinalizedBlock callers; fall back to proven only if the
	// node has not produced a finalized tip yet (early genesis blocks).
	finalized := tips.Finalized
	if finalized.Number == 0 {
		finalized = tips.Proven
	}
	if finalized.Number == 0 {
		return protocol.ZeroBlock{}, nil
	}
	return protocol.NewBlock(
		finalized.Number,
		0,
		blockchain.NewHashIdFromString(finalized.Hash),
		blockchain.EmptyHash,
	), nil
}

func (a *AztecChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	tips := aztec_validations.AztecL2Tips{}
	err := sonic.Unmarshal(blockBytes, &tips)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf(
			"couldn't parse the aztec L2 tips, reason - %s", err.Error(),
		)
	}

	height := tips.Proposed.Number
	if height == 0 {
		return protocol.ZeroBlock{}, fmt.Errorf(
			"couldn't parse the aztec L2 tips, got '%s'", string(blockBytes),
		)
	}

	return protocol.NewBlock(
		height,
		0,
		blockchain.NewHashIdFromString(tips.Proposed.Hash),
		blockchain.EmptyHash,
	), nil
}

func (a *AztecChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, fmt.Errorf("aztec does not support websocket subscriptions")
}

func (a *AztecChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, fmt.Errorf("aztec does not support websocket subscriptions")
}

var _ chains_specific.ChainSpecific = (*AztecChainSpecificObject)(nil)
