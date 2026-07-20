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
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog/log"
)

// errUnsupportedHeadSubscriptions is returned by SubscribeHeadRequest and
// ParseSubscriptionBlock: neither the v2 HTTP API nor the v3 indexer has any
// subscription transport, so head tracking is poll-only.
var errUnsupportedHeadSubscriptions = fmt.Errorf("ton: head subscriptions are not supported")

// tonApiFlavor selects which of TON's two self-contained APIs drives the
// upstream's accounting (head, health, chain validation, labels, lower
// bounds). The v2 HTTP API and the v3 indexer have independent data windows
// and independent failure modes, so a standalone v3 upstream accounts via
// the v3 API; a combined upstream accounts via v2 only.
type tonApiFlavor int

const (
	tonV2 tonApiFlavor = iota
	tonV3
)

type TonChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
	flavor          tonApiFlavor
}

// NewTonChainSpecificObject picks the accounting flavor from the PRIMARY
// (internal-request) connector: rest-indexer means the v3 API drives all
// accounting, anything else means v2. When an upstream combines both APIs,
// the non-primary connector only serves methods - it takes no part in
// validations or calculations - and a warning is logged so the operator
// knows the split deployment (one upstream per API) is the one with full
// per-API accounting.
func NewTonChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	allConnectors []connectors.ApiConnector,
	options *chains.Options,
) *TonChainSpecificObject {
	flavor := tonV2
	primary := "v2"
	if connector != nil && connector.GetType() == specs.RestIndexer {
		flavor = tonV3
		primary = "v3"
	}
	if len(allConnectors) > 1 {
		log.Warn().Msgf(
			"ton upstream '%s' combines the v2 and v3 connectors: all validations and calculations "+
				"(head, health, chain validation, labels, lower bounds) are computed from the primary "+
				"%s API only; the other connector serves methods and nothing else. Deploy each API as "+
				"its own upstream to get independent accounting",
			upstreamId, primary,
		)
	}

	return &TonChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		configuredChain: configuredChain,
		flavor:          flavor,
	}
}

func (t *TonChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return nil
}

func (t *TonChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	var detector labels.ClientLabelsDetector
	if t.flavor == tonV3 {
		detector = ton_labels.NewTonV3ClientLabelsDetector(t.configuredChain.Chain)
	} else {
		detector = ton_labels.NewTonClientLabelsDetector(t.configuredChain.Chain)
	}
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			t.upstreamId,
			t.connector,
			detector,
			t.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(t.ctx, t.upstreamId, labelsDetectors, t.labelsDelay)
}

func (t *TonChainSpecificObject) CapDetectors(input caps.DetectorInput) []caps.CapDetector {
	return caps.DefaultCapDetectors(t.upstreamId, input.WsConnector)
}

func (t *TonChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	var detector lower_bounds.LowerBoundDetector
	if t.flavor == tonV3 {
		detector = ton_bounds.NewTonV3LowerBoundDetector(
			t.upstreamId,
			t.configuredChain.Chain,
			t.internalTimeout,
			t.connector,
		)
	} else {
		detector = ton_bounds.NewTonLowerBoundDetector(
			t.upstreamId,
			t.configuredChain.Chain,
			t.internalTimeout,
			t.connector,
		)
	}
	return lower_bounds.NewBaseLowerBoundProcessor(
		t.ctx,
		t.upstreamId,
		t.configuredChain.AverageRemoveSpeed(),
		[]lower_bounds.LowerBoundDetector{detector},
	)
}

func (t *TonChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if t.options != nil && *t.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	if t.flavor == tonV3 {
		return []validations.Validator[protocol.AvailabilityStatus]{
			ton_validations.NewTonV3HealthValidator(
				t.upstreamId, t.connector, t.configuredChain, t.internalTimeout,
			),
		}
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
	if t.flavor == tonV3 {
		return []validations.Validator[validations.ValidationSettingResult]{
			ton_validations.NewTonV3ChainValidator(t.upstreamId, t.connector, t.configuredChain, t.internalTimeout),
		}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		ton_validations.NewTonChainValidator(t.upstreamId, t.connector, t.configuredChain, t.internalTimeout),
	}
}

// GetLatestBlock polls the accounting API's masterchain head - the
// masterchain is BFT-final, so latest == finalized either way. The v2 and v3
// flavors differ both in path and in envelope (v2 wraps the payload in
// {"ok":true,"result":...}, v3 returns the bare object).
func (t *TonChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	path := "GET#/getMasterchainInfo"
	if t.flavor == tonV3 {
		path = "GET#/api/v3/masterchainInfo"
	}
	request := protocol.NewInternalUpstreamRestRequest(path, nil, t.configuredChain.Chain)
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

// ParseBlock handles both masterchain-info shapes: the v2 toncenter envelope
// {"ok":true,"result":{"last":{...}}} and the v3 bare {"last":{...}}. The
// base64 root_hash is used verbatim as the block id; the parent hash is left
// empty (dshackle fills it with a random string - EmptyHash is the honest
// equivalent, see the near/ripple precedent).
func (t *TonChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	var last tonBlockIdExt
	if t.flavor == tonV3 {
		info := tonV3MasterchainInfo{}
		if err := sonic.Unmarshal(blockBytes, &info); err != nil {
			return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton v3 masterchain info, reason - %s", err.Error())
		}
		last = info.Last
	} else {
		info := tonMasterchainInfoEnvelope{}
		if err := sonic.Unmarshal(blockBytes, &info); err != nil {
			return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton masterchain info, reason - %s", err.Error())
		}
		if !info.Ok {
			return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton masterchain info, got '%s'", string(blockBytes))
		}
		last = info.Result.Last
	}
	if last.RootHash == "" {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton masterchain info, got '%s'", string(blockBytes))
	}

	return protocol.NewBlock(
		last.Seqno,
		0,
		blockchain.NewHashIdFromString(last.RootHash),
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

type tonV3MasterchainInfo struct {
	Last tonBlockIdExt `json:"last"`
}

type tonBlockIdExt struct {
	Seqno    uint64 `json:"seqno"`
	RootHash string `json:"root_hash"`
}

var _ chains_specific.ChainSpecific = (*TonChainSpecificObject)(nil)
