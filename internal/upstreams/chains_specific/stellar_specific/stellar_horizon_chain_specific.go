package stellar_specific

import (
	"context"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
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
)

// StellarHorizonChainSpecificObject drives an upstream through the Horizon
// REST API.
type StellarHorizonChainSpecificObject struct {
	stellarBaseChainSpecificObject
}

func NewStellarHorizonChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	pollInterval time.Duration,
	options *chains.Options,
) *StellarHorizonChainSpecificObject {
	return &StellarHorizonChainSpecificObject{
		stellarBaseChainSpecificObject: newStellarBaseChainSpecificObject(ctx, configuredChain, upstreamId, connector, pollInterval, options),
	}
}

func (s *StellarHorizonChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return s.newStellarBlockProcessor(s)
}

func (s *StellarHorizonChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			s.upstreamId,
			s.connector,
			stellar_labels.NewStellarHorizonClientLabelsDetector(s.configuredChain.Chain),
			s.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(s.ctx, s.upstreamId, labelsDetectors, s.labelsDelay)
}

func (s *StellarHorizonChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		stellar_bounds.NewStellarHorizonLowerBoundDetector(
			s.upstreamId,
			s.configuredChain.Chain,
			s.internalTimeout,
			s.connector,
		),
	}
	return lower_bounds.NewBaseLowerBoundProcessor(
		s.ctx,
		s.upstreamId,
		s.configuredChain.AverageRemoveSpeed(),
		detectors,
	)
}

func (s *StellarHorizonChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if s.options != nil && *s.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{
		stellar_validations.NewStellarHorizonSyncingValidator(
			s.upstreamId, s.connector, s.configuredChain, s.internalTimeout,
		),
	}
}

func (s *StellarHorizonChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if s.configuredChain == nil || s.configuredChain.ChainId == "" {
		return nil
	}
	if s.options != nil && *s.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		stellar_validations.NewStellarHorizonChainValidator(s.upstreamId, s.connector, s.configuredChain, s.internalTimeout),
	}
}

// GetLatestBlock polls the newest history ledger via
// GET /ledgers?order=desc&limit=1 (the root endpoint has no ledger hash) -
// SCP closes ledgers with immediate finality, so the latest ledger is also
// the finalized one.
func (s *StellarHorizonChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
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

func (s *StellarHorizonChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return s.GetLatestBlock(ctx)
}

// ParseBlock expects Horizon's HAL ledgers page:
// {"_embedded":{"records":[{"hash":"...","prev_hash":"...","sequence":N}]}}.
func (s *StellarHorizonChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
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

var _ chains_specific.ChainSpecific = (*StellarHorizonChainSpecificObject)(nil)
