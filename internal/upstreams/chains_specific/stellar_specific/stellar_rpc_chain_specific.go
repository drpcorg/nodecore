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

// StellarRpcChainSpecificObject drives an upstream through the stellar-rpc
// JSON-RPC API.
type StellarRpcChainSpecificObject struct {
	stellarBaseChainSpecificObject
}

func NewStellarRpcChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	pollInterval time.Duration,
	options *chains.Options,
) *StellarRpcChainSpecificObject {
	return &StellarRpcChainSpecificObject{
		stellarBaseChainSpecificObject: newStellarBaseChainSpecificObject(ctx, configuredChain, upstreamId, connector, pollInterval, options),
	}
}

func (s *StellarRpcChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return s.newStellarBlockProcessor(s)
}

func (s *StellarRpcChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			s.upstreamId,
			s.connector,
			stellar_labels.NewStellarClientLabelsDetector(s.configuredChain.Chain),
			s.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(s.ctx, s.upstreamId, labelsDetectors, s.labelsDelay)
}

func (s *StellarRpcChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		stellar_bounds.NewStellarLowerBoundDetector(
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

func (s *StellarRpcChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if s.options != nil && *s.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{
		stellar_validations.NewStellarSyncingValidator(
			s.upstreamId, s.connector, s.configuredChain, s.internalTimeout,
		),
	}
}

func (s *StellarRpcChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if s.configuredChain == nil || s.configuredChain.ChainId == "" {
		return nil
	}
	if s.options != nil && *s.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		stellar_validations.NewStellarChainValidator(s.upstreamId, s.connector, s.configuredChain, s.internalTimeout),
	}
}

// GetLatestBlock polls getLatestLedger - SCP closes ledgers with immediate
// finality, so the latest ledger is also the finalized one.
func (s *StellarRpcChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
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

func (s *StellarRpcChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return s.GetLatestBlock(ctx)
}

// ParseBlock expects stellar-rpc's getLatestLedger result:
// {"id":"<hex hash>","sequence":N,...}.
func (s *StellarRpcChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
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

type stellarLatestLedger struct {
	Id       string `json:"id"`
	Sequence uint64 `json:"sequence"`
}

var _ chains_specific.ChainSpecific = (*StellarRpcChainSpecificObject)(nil)
