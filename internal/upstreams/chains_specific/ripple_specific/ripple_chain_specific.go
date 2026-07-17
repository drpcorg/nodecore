package ripple_specific

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
	"github.com/drpcorg/nodecore/internal/upstreams/labels/ripple_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/ripple_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/ripple_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// errUnsupportedHeadSubscriptions is returned by SubscribeHeadRequest and
// ParseSubscriptionBlock: XRPL subscriptions exist only over WebSocket with a
// distinct {"command"} framing that v1 does not implement, so head tracking
// is poll-only.
var errUnsupportedHeadSubscriptions = fmt.Errorf("ripple: head subscriptions are not supported")

type RippleChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewRippleChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	options *chains.Options,
) *RippleChainSpecificObject {
	return &RippleChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		configuredChain: configuredChain,
	}
}

func (r *RippleChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return nil
}

func (r *RippleChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			r.upstreamId,
			r.connector,
			ripple_labels.NewRippleClientLabelsDetector(r.configuredChain.Chain),
			r.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(r.ctx, r.upstreamId, labelsDetectors, r.labelsDelay)
}

func (r *RippleChainSpecificObject) CapDetectors(input caps.DetectorInput) []caps.CapDetector {
	return caps.DefaultCapDetectors(r.upstreamId, input.WsConnector)
}

func (r *RippleChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		ripple_bounds.NewRippleLowerBoundDetector(
			r.upstreamId,
			r.configuredChain.Chain,
			r.internalTimeout,
			r.connector,
		),
	}
	return lower_bounds.NewBaseLowerBoundProcessor(
		r.ctx,
		r.upstreamId,
		r.configuredChain.AverageRemoveSpeed(),
		detectors,
	)
}

func (r *RippleChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if r.options != nil && *r.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	validatePeers := r.options != nil && r.options.ValidatePeers != nil && *r.options.ValidatePeers
	return []validations.Validator[protocol.AvailabilityStatus]{
		ripple_validations.NewRippleHealthValidator(
			r.upstreamId, r.connector, r.configuredChain, r.internalTimeout, validatePeers,
		),
	}
}

func (r *RippleChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if r.configuredChain == nil || r.configuredChain.ChainId == "" {
		return nil
	}
	if r.options != nil && *r.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		ripple_validations.NewRippleChainValidator(r.upstreamId, r.connector, r.configuredChain, r.internalTimeout),
	}
}

// GetLatestBlock polls the validated ledger - deliberately not ledger_closed
// (dshackle's choice), which returns the most recently closed but possibly
// not-yet-validated ledger. The validated head is XRPL finality and matches
// the WS ledger-stream semantics.
func (r *RippleChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"ledger",
		[]any{map[string]any{"ledger_index": "validated"}},
		r.configuredChain.Chain,
	)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	response := r.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	block, err := r.ParseBlock(response.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ripple validated ledger: %w", err)
	}
	return block, nil
}

// GetFinalizedBlock delegates to GetLatestBlock: the validated ledger is
// XRPL finality, there is no later stage.
func (r *RippleChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return r.GetLatestBlock(ctx)
}

// ParseBlock expects the payload shape of the `ledger` method:
// {"ledger":{"parent_hash":...}, "ledger_hash":"...", "ledger_index":N,
// "validated":true, "status":"success"} - ledger_index is a number at the
// top level (it is a string inside the nested ledger object).
func (r *RippleChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	payload := rippleLedgerResult{}
	if err := sonic.Unmarshal(blockBytes, &payload); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ripple ledger, reason - %s", err.Error())
	}
	if payload.LedgerHash == "" {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ripple ledger, got '%s'", string(blockBytes))
	}

	parentHash := blockchain.EmptyHash
	if payload.Ledger.ParentHash != "" {
		parentHash = blockchain.NewHashIdFromString(payload.Ledger.ParentHash)
	}

	return protocol.NewBlock(
		payload.LedgerIndex,
		0,
		blockchain.NewHashIdFromString(payload.LedgerHash),
		parentHash,
	), nil
}

func (r *RippleChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedHeadSubscriptions
}

func (r *RippleChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, errUnsupportedHeadSubscriptions
}

type rippleLedgerResult struct {
	Ledger      rippleLedgerHeader `json:"ledger"`
	LedgerHash  string             `json:"ledger_hash"`
	LedgerIndex uint64             `json:"ledger_index"`
}

type rippleLedgerHeader struct {
	ParentHash string `json:"parent_hash"`
}

var _ chains_specific.ChainSpecific = (*RippleChainSpecificObject)(nil)
