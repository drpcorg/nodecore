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

var errStellarNoLatestLedger = errors.New("stellar node reported no latestLedger")

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
		stellarBaseChainSpecificObject: newStellarBaseChainSpecificObject(
			ctx, configuredChain, upstreamId, connector, pollInterval, options,
		),
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
	return labels.NewGenericLabelsProcessor(s.ctx, s.upstreamId, labelsDetectors, s.labelsDelay)
}

func (s *StellarRpcChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		stellar_bounds.NewStellarLowerBoundDetector(
			s.upstreamId, s.configuredChain.Chain, s.internalTimeout, s.connector,
		),
	}
	return lower_bounds.NewGenericLowerBoundProcessor(
		s.ctx, s.upstreamId, s.configuredChain.AverageRemoveSpeed(), detectors,
	)
}

func (s *StellarRpcChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	validators := make([]validations.Validator[protocol.AvailabilityStatus], 0, 1)
	if *s.options.ValidateSyncing {
		validators = append(validators, stellar_validations.NewStellarSyncingValidator(
			s.upstreamId, s.connector, s.configuredChain, s.internalTimeout,
		))
	}
	return validators
}

func (s *StellarRpcChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if s.configuredChain.ChainId == "" {
		return nil
	}
	if *s.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		stellar_validations.NewStellarChainValidator(
			s.upstreamId, s.connector, s.configuredChain, s.internalTimeout,
		),
	}
}

// GetLatestBlock polls getHealth for the head. getLatestLedger is deliberately
// not used: it carries the ledger header XDR, exposes no parent hash either,
// and getHealth is the same small document the lower-bound detector reads.
// A node that has tripped its own >30s staleness check answers getHealth with
// an error, so the head stops advancing rather than reporting a stale ledger -
// the health validator marks that upstream Unavailable on the same signal.
func (s *StellarRpcChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	health, err := specific_helpers.FetchStellarHealth(ctx, s.connector, s.configuredChain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return newStellarBlock(health.LatestLedger)
}

// GetFinalizedBlock - SCP closes ledgers with immediate finality, so the head
// is also the finalized ledger.
func (s *StellarRpcChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return s.GetLatestBlock(ctx)
}

// ParseBlock expects a getHealth result: {"status":...,"latestLedger":N,...}.
func (s *StellarRpcChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	health, err := specific_helpers.ParseStellarHealth(blockBytes)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the stellar getHealth result, reason - %s", err.Error())
	}
	return newStellarBlock(health.LatestLedger)
}

// newStellarBlock builds a head block from a ledger sequence. The sequence is
// the only identity either API exposes cheaply, so the hashes are synthetic -
// derived identically here and on the Horizon path, which keeps head hashes
// linkable across a pool that mixes both APIs.
func newStellarBlock(sequence uint64) (protocol.Block, error) {
	if sequence == 0 {
		return protocol.ZeroBlock{}, errStellarNoLatestLedger
	}
	hash, parentHash := specific_helpers.SyntheticHashes(sequence, sequence-1)
	return protocol.NewBlock(sequence, 0, hash, parentHash), nil
}

var _ chains_specific.ChainSpecific = (*StellarRpcChainSpecificObject)(nil)
