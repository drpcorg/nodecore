package ton_specific

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
	"github.com/drpcorg/nodecore/internal/upstreams/labels/ton_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/ton_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/ton_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// errUnsupportedHeadSubscriptions is returned by SubscribeHeadRequest and
// ParseSubscriptionBlock: neither the v2 HTTP API nor the v3 indexer has any
// subscription transport, so head tracking is poll-only.
var errUnsupportedHeadSubscriptions = fmt.Errorf("ton: head subscriptions are not supported")

type TonChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewTonChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	options *chains.Options,
) *TonChainSpecificObject {
	return &TonChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		configuredChain: configuredChain,
	}
}

func (t *TonChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return nil
}

func (t *TonChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			t.upstreamId,
			t.connector,
			ton_labels.NewTonClientLabelsDetector(t.configuredChain.Chain),
			t.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(t.ctx, t.upstreamId, labelsDetectors, t.labelsDelay)
}

func (t *TonChainSpecificObject) CapDetectors(input caps.DetectorInput) []caps.CapDetector {
	return caps.DefaultCapDetectors(t.upstreamId, input.WsConnector)
}

func (t *TonChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		ton_bounds.NewTonLowerBoundDetector(
			t.upstreamId,
			t.configuredChain.Chain,
			t.internalTimeout,
			t.connector,
		),
	}
	return lower_bounds.NewBaseLowerBoundProcessor(
		t.ctx,
		t.upstreamId,
		t.configuredChain.AverageRemoveSpeed(),
		detectors,
	)
}

func (t *TonChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if t.options != nil && *t.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{
		ton_validations.NewTonHealthValidator(
			t.upstreamId, t.connector, t.configuredChain, t.internalTimeout,
		),
	}
}

func (t *TonChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if t.configuredChain == nil || t.configuredChain.ChainId == "" {
		return nil
	}
	if t.options != nil && *t.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		ton_validations.NewTonChainValidator(t.upstreamId, t.connector, t.configuredChain, t.internalTimeout),
	}
}

// GetLatestBlock polls GET /getMasterchainInfo - the masterchain head is
// BFT-final, so latest == finalized on TON.
func (t *TonChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	request := protocol.NewInternalUpstreamRestRequest("GET#/getMasterchainInfo", nil, t.configuredChain.Chain)
	response := t.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	block, err := t.ParseBlock(response.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton masterchain info: %w", err)
	}
	return block, nil
}

func (t *TonChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return t.GetLatestBlock(ctx)
}

// ParseBlock expects the toncenter envelope of getMasterchainInfo:
// {"ok":true,"result":{"last":{"seqno":N,"root_hash":"<base64>",...},...}}.
// The base64 root_hash is used verbatim as the block id; the parent hash is
// left empty (dshackle fills it with a random string - EmptyHash is the
// honest equivalent, see the near/ripple precedent).
func (t *TonChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	info := tonMasterchainInfoEnvelope{}
	if err := sonic.Unmarshal(blockBytes, &info); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton masterchain info, reason - %s", err.Error())
	}
	if !info.Ok || info.Result.Last.RootHash == "" {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton masterchain info, got '%s'", string(blockBytes))
	}

	return protocol.NewBlock(
		info.Result.Last.Seqno,
		0,
		blockchain.NewHashIdFromString(info.Result.Last.RootHash),
		blockchain.EmptyHash,
	), nil
}

func (t *TonChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedHeadSubscriptions
}

func (t *TonChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, errUnsupportedHeadSubscriptions
}

type tonMasterchainInfoEnvelope struct {
	Ok     bool                 `json:"ok"`
	Result tonMasterchainResult `json:"result"`
}

type tonMasterchainResult struct {
	Last tonBlockIdExt `json:"last"`
}

type tonBlockIdExt struct {
	Seqno    uint64 `json:"seqno"`
	RootHash string `json:"root_hash"`
}

var _ chains_specific.ChainSpecific = (*TonChainSpecificObject)(nil)
