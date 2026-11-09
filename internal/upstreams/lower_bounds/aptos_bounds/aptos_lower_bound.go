package aptos_bounds

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/aptos_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const aptosPeriod = 5 * time.Minute

var errAptosMissingBoundField = errors.New("field is missing")

type AptosLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration

	lastVersion atomic.Int64
	lastBlock   atomic.Int64
}

func NewAptosLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *AptosLowerBoundDetector {
	return &AptosLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

// DetectLowerBound reads the oldest retained ledger version and block height
// straight from GET /v1 - Aptos exposes both directly, so no binary search is
// needed (unlike Algorand).
//
// The version bound is published as SlotBound, not StateBound, because it lives
// in ledger-version space - roughly 7x the block height, which is what the head
// reports. A bound above the head is discarded as bogus (see
// LowerBoundUpstreamStateEvent.Same), and SlotBound is the one type exempt from
// that comparison, so a StateBound here never reached the router and every
// version-addressed request was rejected for having no bound data. Slot is also
// the axis the head's own Slot field already carries for Aptos. BlockBound stays
// in block-height space.
func (a *AptosLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	info, err := a.fetchLedgerInfo(ctx)
	if err != nil {
		return a.fallback(err), nil
	}
	version, err := parseBound(info.OldestLedgerVersion)
	if err != nil {
		return a.fallback(fmt.Errorf("invalid oldest_ledger_version '%s': %w", info.OldestLedgerVersion, err)), nil
	}
	block, err := parseBound(info.OldestBlockHeight)
	if err != nil {
		return a.fallback(fmt.Errorf("invalid oldest_block_height '%s': %w", info.OldestBlockHeight, err)), nil
	}
	a.lastVersion.Store(version)
	a.lastBlock.Store(block)
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(version, protocol.SlotBound),
		protocol.NewLowerBoundDataNow(block, protocol.BlockBound),
	}, nil
}

// parseBound parses an oldest_* field. A missing field means a failed
// detection, not an archive claim. A genuine 0 (a full-archive node - the
// normal mainnet case) is clamped to 1: bound 1 is the predictor's "archive"
// special case, while a raw 0 reads as "unknown" in bound matchers.
func parseBound(s string) (int64, error) {
	if s == "" {
		return 0, errAptosMissingBoundField
	}
	value, err := strconv.ParseUint(s, 10, 63)
	if err != nil {
		return 0, err
	}
	return max(int64(value), 1), nil
}

// fallback decides what to publish when detection cannot complete. If a
// previous tick produced bounds, re-emit them so the router keeps using the
// last known good values; otherwise emit UnknownBound=0 as an explicit
// "we don't know" signal.
func (a *AptosLowerBoundDetector) fallback(reason error) []protocol.LowerBoundData {
	version, block := a.lastVersion.Load(), a.lastBlock.Load()
	if version > 0 && block > 0 {
		log.Warn().Err(reason).Msgf(
			"aptos upstream '%s' lower-bound fetch failed; retaining cached SLOT=%d BLOCK=%d",
			a.upstreamId, version, block,
		)
		return []protocol.LowerBoundData{
			protocol.NewLowerBoundDataNow(version, protocol.SlotBound),
			protocol.NewLowerBoundDataNow(block, protocol.BlockBound),
		}
	}
	log.Warn().Err(reason).Msgf(
		"aptos upstream '%s' lower-bound fetch failed and no cache available; emitting UnknownBound",
		a.upstreamId,
	)
	return []protocol.LowerBoundData{protocol.NewLowerBoundDataNow(0, protocol.UnknownBound)}
}

func (a *AptosLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.SlotBound, protocol.BlockBound, protocol.UnknownBound}
}

func (a *AptosLowerBoundDetector) Period() time.Duration {
	return aptosPeriod
}

func (a *AptosLowerBoundDetector) fetchLedgerInfo(ctx context.Context) (*aptos_validations.AptosLedgerInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, a.internalTimeout)
	defer cancel()

	return aptos_validations.FetchLedgerInfo(ctx, a.connector, a.chain)
}

var _ lower_bounds.LowerBoundDetector = (*AptosLowerBoundDetector)(nil)
