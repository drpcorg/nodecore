package stellar_specific

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
	"github.com/drpcorg/nodecore/internal/upstreams/labels/stellar_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/stellar_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/stellar_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog/log"
)

// errUnsupportedHeadSubscriptions is returned by SubscribeHeadRequest and
// ParseSubscriptionBlock: stellar-rpc is HTTP POST JSON-RPC only, and
// Horizon's SSE streaming is out of v1 scope, so head tracking is poll-only.
var errUnsupportedHeadSubscriptions = fmt.Errorf("stellar: head subscriptions are not supported")

// stellarApiFlavor selects which of Stellar's two self-contained APIs drives
// the upstream's accounting (head, health, chain validation, labels, lower
// bounds): stellar-rpc (json-rpc connector) or Horizon (rest connector).
// They have independent data windows and failure modes, so a standalone
// Horizon upstream accounts via Horizon; a combined upstream accounts via
// the primary connector only.
type stellarApiFlavor int

const (
	stellarRpc stellarApiFlavor = iota
	stellarHorizon
)

type StellarChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
	flavor          stellarApiFlavor
}

// NewStellarChainSpecificObject picks the accounting flavor from the PRIMARY
// (internal-request) connector: rest means Horizon drives all accounting,
// anything else means stellar-rpc. When an upstream combines both APIs, the
// non-primary connector only serves methods - it takes no part in
// validations or calculations - and a warning is logged so the operator
// knows the split deployment (one upstream per API) is the one with full
// per-API accounting.
func NewStellarChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	allConnectors []connectors.ApiConnector,
	options *chains.Options,
) *StellarChainSpecificObject {
	flavor := stellarRpc
	primary := "stellar-rpc"
	if connector != nil && connector.GetType() == specs.RestConnector {
		flavor = stellarHorizon
		primary = "horizon"
	}
	if len(allConnectors) > 1 {
		log.Warn().Msgf(
			"stellar upstream '%s' combines the stellar-rpc and horizon connectors: all validations and "+
				"calculations (head, health, chain validation, labels, lower bounds) are computed from the "+
				"primary %s API only; the other connector serves methods and nothing else. Deploy each API "+
				"as its own upstream to get independent accounting",
			upstreamId, primary,
		)
	}

	return &StellarChainSpecificObject{
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

func (s *StellarChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return nil
}

func (s *StellarChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	var detector labels.ClientLabelsDetector
	if s.flavor == stellarHorizon {
		detector = stellar_labels.NewStellarHorizonClientLabelsDetector(s.configuredChain.Chain)
	} else {
		detector = stellar_labels.NewStellarClientLabelsDetector(s.configuredChain.Chain)
	}
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			s.upstreamId,
			s.connector,
			detector,
			s.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(s.ctx, s.upstreamId, labelsDetectors, s.labelsDelay)
}

func (s *StellarChainSpecificObject) CapDetectors(input caps.DetectorInput) []caps.CapDetector {
	return caps.DefaultCapDetectors(s.upstreamId, input.WsConnector)
}

func (s *StellarChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	var detector lower_bounds.LowerBoundDetector
	if s.flavor == stellarHorizon {
		detector = stellar_bounds.NewStellarHorizonLowerBoundDetector(
			s.upstreamId,
			s.configuredChain.Chain,
			s.internalTimeout,
			s.connector,
		)
	} else {
		detector = stellar_bounds.NewStellarLowerBoundDetector(
			s.upstreamId,
			s.configuredChain.Chain,
			s.internalTimeout,
			s.connector,
		)
	}
	return lower_bounds.NewBaseLowerBoundProcessor(
		s.ctx,
		s.upstreamId,
		s.configuredChain.AverageRemoveSpeed(),
		[]lower_bounds.LowerBoundDetector{detector},
	)
}

func (s *StellarChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if s.options != nil && *s.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	if s.flavor == stellarHorizon {
		return []validations.Validator[protocol.AvailabilityStatus]{
			stellar_validations.NewStellarHorizonHealthValidator(
				s.upstreamId, s.connector, s.configuredChain, s.internalTimeout,
			),
		}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{
		stellar_validations.NewStellarHealthValidator(
			s.upstreamId, s.connector, s.configuredChain, s.internalTimeout,
		),
	}
}

func (s *StellarChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if s.configuredChain == nil || s.configuredChain.ChainId == "" {
		return nil
	}
	if s.options != nil && *s.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	if s.flavor == stellarHorizon {
		return []validations.Validator[validations.ValidationSettingResult]{
			stellar_validations.NewStellarHorizonChainValidator(s.upstreamId, s.connector, s.configuredChain, s.internalTimeout),
		}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		stellar_validations.NewStellarChainValidator(s.upstreamId, s.connector, s.configuredChain, s.internalTimeout),
	}
}

// GetLatestBlock polls the accounting API's head - SCP closes ledgers with
// immediate finality, so the latest ledger is also the finalized one on both
// APIs. stellar-rpc: getLatestLedger; Horizon: the newest history ledger via
// GET /ledgers?order=desc&limit=1 (the root endpoint has no ledger hash).
func (s *StellarChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	if s.flavor == stellarHorizon {
		return s.getLatestHorizonLedger(ctx)
	}
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"getLatestLedger",
		map[string]any{},
		s.configuredChain.Chain,
	)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	block, err := s.ParseBlock(response.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the stellar latest ledger: %w", err)
	}
	return block, nil
}

func (s *StellarChainSpecificObject) getLatestHorizonLedger(ctx context.Context) (protocol.Block, error) {
	request := protocol.NewInternalUpstreamRestRequest(
		"GET#/ledgers",
		&protocol.RequestParams{QueryParams: map[string][]string{"order": {"desc"}, "limit": {"1"}}},
		s.configuredChain.Chain,
	)
	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	block, err := s.ParseBlock(response.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the horizon latest ledger: %w", err)
	}
	return block, nil
}

func (s *StellarChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return s.GetLatestBlock(ctx)
}

// ParseBlock handles both head shapes: stellar-rpc's getLatestLedger result
// {"id":"<hex hash>","sequence":N,...} and Horizon's HAL ledgers page
// {"_embedded":{"records":[{"hash":"...","prev_hash":"...","sequence":N}]}}.
func (s *StellarChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	if s.flavor == stellarHorizon {
		page := horizonLedgersPage{}
		if err := sonic.Unmarshal(blockBytes, &page); err != nil {
			return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the horizon ledgers page, reason - %s", err.Error())
		}
		if len(page.Embedded.Records) == 0 || page.Embedded.Records[0].Hash == "" {
			return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the horizon ledgers page, got '%s'", string(blockBytes))
		}
		record := page.Embedded.Records[0]
		parentHash := blockchain.EmptyHash
		if record.PrevHash != "" {
			parentHash = blockchain.NewHashIdFromString(record.PrevHash)
		}
		return protocol.NewBlock(
			record.Sequence,
			0,
			blockchain.NewHashIdFromString(record.Hash),
			parentHash,
		), nil
	}

	ledger := stellarLatestLedger{}
	if err := sonic.Unmarshal(blockBytes, &ledger); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the stellar latest ledger, reason - %s", err.Error())
	}
	if ledger.Id == "" {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the stellar latest ledger, got '%s'", string(blockBytes))
	}

	return protocol.NewBlock(
		ledger.Sequence,
		0,
		blockchain.NewHashIdFromString(ledger.Id),
		blockchain.EmptyHash,
	), nil
}

func (s *StellarChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedHeadSubscriptions
}

func (s *StellarChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, errUnsupportedHeadSubscriptions
}

type stellarLatestLedger struct {
	Id       string `json:"id"`
	Sequence uint64 `json:"sequence"`
}

type horizonLedgersPage struct {
	Embedded horizonLedgersEmbedded `json:"_embedded"`
}

type horizonLedgersEmbedded struct {
	Records []horizonLedgerRecord `json:"records"`
}

type horizonLedgerRecord struct {
	Hash     string `json:"hash"`
	PrevHash string `json:"prev_hash"`
	Sequence uint64 `json:"sequence"`
}

var _ chains_specific.ChainSpecific = (*StellarChainSpecificObject)(nil)
