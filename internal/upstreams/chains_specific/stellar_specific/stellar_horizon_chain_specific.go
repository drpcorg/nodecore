package stellar_specific

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/stellar_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/stellar_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/stellar_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
)

var errStellarHorizonNoLatestLedger = errors.New("horizon reported no history_latest_ledger")

// StellarHorizonChainSpecificObject drives an upstream through the Horizon REST
// API. Everything it needs - head, passphrase, version, history boundary -
// comes from the root document, so a standalone Horizon upstream keeps full
// accounting from a single endpoint.
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
		stellarBaseChainSpecificObject: newStellarBaseChainSpecificObject(
			ctx, configuredChain, upstreamId, connector, pollInterval, options,
		),
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
	return labels.NewGenericLabelsProcessor(s.ctx, s.upstreamId, labelsDetectors, s.labelsDelay)
}

func (s *StellarHorizonChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		stellar_bounds.NewStellarHorizonLowerBoundDetector(
			s.upstreamId, s.configuredChain.Chain, s.internalTimeout, s.connector,
		),
	}
	return lower_bounds.NewGenericLowerBoundProcessor(
		s.ctx, s.upstreamId, s.configuredChain.AverageRemoveSpeed(), detectors,
	)
}

func (s *StellarHorizonChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	validators := make([]validations.Validator[protocol.AvailabilityStatus], 0, 1)
	if *s.options.ValidateSyncing {
		validators = append(validators, stellar_validations.NewStellarHorizonSyncingValidator(
			s.upstreamId, s.connector, s.configuredChain, s.internalTimeout,
		))
	}
	return validators
}

func (s *StellarHorizonChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if s.configuredChain.ChainId == "" {
		return nil
	}
	if *s.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		stellar_validations.NewStellarHorizonChainValidator(
			s.upstreamId, s.connector, s.configuredChain, s.internalTimeout,
		),
	}
}

// GetLatestBlock reads history_latest_ledger from the root document - the
// newest ledger Horizon can actually serve, and the same document that carries
// the passphrase, the version and the history boundary.
func (s *StellarHorizonChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	root, err := specific_helpers.FetchStellarHorizonRoot(ctx, s.connector, s.configuredChain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return newStellarHorizonBlock(root.HistoryLatestLedger)
}

// GetFinalizedBlock - SCP closes ledgers with immediate finality, so the head
// is also the finalized ledger.
func (s *StellarHorizonChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return s.GetLatestBlock(ctx)
}

// ParseBlock expects Horizon's root document.
func (s *StellarHorizonChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	root, err := specific_helpers.ParseStellarHorizonRoot(blockBytes)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the horizon root document, reason - %s", err.Error())
	}
	return newStellarHorizonBlock(root.HistoryLatestLedger)
}

func newStellarHorizonBlock(sequence uint64) (protocol.Block, error) {
	if sequence == 0 {
		return protocol.ZeroBlock{}, errStellarHorizonNoLatestLedger
	}
	return newStellarBlock(sequence)
}

var _ chains_specific.ChainSpecific = (*StellarHorizonChainSpecificObject)(nil)
