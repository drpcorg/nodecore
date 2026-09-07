package evm_bounds

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const evmProofsSyncStatusMethod = "debug_proofsSyncStatus"

// EvmProofLowerBoundDetector detects the earliest block eth_getProof still serves. Proofs
// have their own pipeline because eth_capabilities is unreliable for them: op-reth reports
// stateproofs.oldestBlock at the head while --proofs-history serves a window 129600 blocks
// deep. Sources in order, every cycle, with no cached verdicts:
//  1. debug_proofsSyncStatus (op-reth): earliest of the reported window.
//  2. eth_capabilities: stateproofs.oldestBlock, trusted only when it is below head.number.
//  3. eth_getProof binary search.
type EvmProofLowerBoundDetector struct {
	*lower_bounds.LowerBoundSearchCalculator
	evmRpcClient

	capabilities *EvmCapabilities
}

func NewEvmProofLowerBoundDetector(
	upstreamId string,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmProofLowerBoundDetector {
	return &EvmProofLowerBoundDetector{
		LowerBoundSearchCalculator: lower_bounds.NewLowerBoundSearchCalculatorWithOffset(
			upstreamId,
			protocol.ProofBound,
			[]protocol.LowerBoundType{protocol.ProofBound},
			evmLowerBoundPeriod,
			0,
		),
		evmRpcClient: evmRpcClient{connector: connector, chain: chain, internalTimeout: internalTimeout},
	}
}

// WithCapabilities attaches the upstream-shared eth_capabilities cache. Detectors
// without one (nil) go straight from the sync status to the search.
func (e *EvmProofLowerBoundDetector) WithCapabilities(capabilities *EvmCapabilities) *EvmProofLowerBoundDetector {
	e.capabilities = capabilities
	return e
}

func (e *EvmProofLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	if bound, ok := e.detectFromProofsSyncStatus(ctx); ok {
		return e.LowerBoundResults(bound), nil
	}
	if results, ok := e.detectFromCapabilities(ctx); ok {
		return results, nil
	}
	return e.LowerBoundSearchCalculator.DetectLowerBound(ctx, e.fetchLatestHeight, e.hasProof)
}

// detectFromProofsSyncStatus asks the upstream for the block window its historical proof
// store serves: one call replaces the eth_getProof binary search. Nothing is remembered
// between cycles - an upstream without the method rejects one request per detection cycle,
// which is cheaper than a cached verdict with its own re-probe timer. The window's upper
// edge is not published: measured op-reth stores keep it at the head.
func (e *EvmProofLowerBoundDetector) detectFromProofsSyncStatus(ctx context.Context) (int64, bool) {
	raw, available, err := e.call(ctx, evmProofsSyncStatusMethod, []any{})
	if err != nil {
		log.Debug().Err(err).Msgf("couldn't fetch %s from upstream '%s'", evmProofsSyncStatusMethod, e.UpstreamId)
		return 0, false
	}
	if !available {
		return 0, false
	}
	var window struct {
		Earliest json.RawMessage `json:"earliest"`
		Latest   json.RawMessage `json:"latest"`
	}
	if err := sonic.Unmarshal(raw, &window); err != nil || len(window.Earliest) == 0 || len(window.Latest) == 0 {
		log.Debug().Err(err).Msgf("unable to parse %s of upstream '%s': %s", evmProofsSyncStatusMethod, e.UpstreamId, raw)
		return 0, false
	}
	earliest, earliestErr := parseEvmBlockNumber(window.Earliest)
	latest, latestErr := parseEvmBlockNumber(window.Latest)
	if earliestErr != nil || latestErr != nil || earliest < 0 || latest <= 0 || earliest > latest {
		log.Debug().Msgf("upstream '%s' reports no usable proof window: %s", e.UpstreamId, raw)
		return 0, false
	}
	// earliest 0 is coerced to 1: a 0 bound reads as "unknown" to routing
	if earliest == 0 {
		return 1, true
	}
	return earliest, true
}

// detectFromCapabilities reads stateproofs from the upstream-shared eth_capabilities
// report. The value is trusted only below the head reported in the same response:
// op-reth answers oldestBlock == head.number while serving a much deeper window, and a
// report without a head cannot be validated at all. Both cases go to the search.
func (e *EvmProofLowerBoundDetector) detectFromCapabilities(ctx context.Context) ([]protocol.LowerBoundData, bool) {
	if e.capabilities == nil {
		return nil, false
	}
	snapshot := e.capabilities.snapshot(ctx)
	if snapshot == nil {
		return nil, false
	}
	res, covered := snapshot.resource(protocol.ProofBound)
	if !covered {
		return nil, false
	}
	// A disabled resource is covered but yields no bound: routing treats an absent bound
	// as "this upstream has no data of that type" and excludes it.
	if res.disabled {
		return []protocol.LowerBoundData{}, true
	}
	if snapshot.head == 0 || res.bound >= snapshot.head {
		log.Debug().Msgf(
			"upstream '%s' %s reports proofs from %d with head %d, ignoring it for the proof bound",
			e.UpstreamId, evmCapabilitiesMethod, res.bound, snapshot.head,
		)
		return nil, false
	}
	return e.LowerBoundResults(res.bound), true
}

func (e *EvmProofLowerBoundDetector) hasProof(ctx context.Context, height int64) (bool, error) {
	raw, available, err := e.call(ctx, "eth_getProof", []any{evmZeroAddress, []string{}, evmBlockTag(height)})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}

var _ lower_bounds.LowerBoundDetector = (*EvmProofLowerBoundDetector)(nil)
