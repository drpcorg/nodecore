package stellar_bounds

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// Horizon's retention reaper slides history_elder_ledger up continuously on
// retention-limited deployments; re-poll often to keep the bound close to the
// real history boundary.
const stellarHorizonPeriod = 2 * time.Minute

var errStellarHorizonNoElderLedger = errors.New("horizon reported no history_elder_ledger")

// No StateBound: horizon serves ledger/tx history, current state comes from
// captive core with no historical depth.
var stellarHorizonEmittedBoundTypes = []protocol.LowerBoundType{
	protocol.BlockBound,
	protocol.TxBound,
}

type StellarHorizonLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration

	lastBound atomic.Uint64
}

func NewStellarHorizonLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *StellarHorizonLowerBoundDetector {
	return &StellarHorizonLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *StellarHorizonLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	elder, err := s.fetchElderLedger(ctx)
	if err != nil {
		return s.fallback(err), nil
	}
	if elder == 0 {
		// Zero or absent means the node did not report its history
		// boundary, not that the full history is available.
		return s.fallback(errStellarHorizonNoElderLedger), nil
	}
	s.lastBound.Store(elder)

	return stellarHorizonBounds(elder), nil
}

func (s *StellarHorizonLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return append(append([]protocol.LowerBoundType{}, stellarHorizonEmittedBoundTypes...), protocol.UnknownBound)
}

func (s *StellarHorizonLowerBoundDetector) Period() time.Duration {
	return stellarHorizonPeriod
}

// AllowsBoundDecrease opts this detector out of the processor's monotonic
// filter: the retention reaper moves history_elder_ledger UP, but a
// `horizon db reingest range` backfill legally moves it DOWN.
func (s *StellarHorizonLowerBoundDetector) AllowsBoundDecrease() bool {
	return true
}

// fallback decides what to publish when the probe fails or the node
// advertises no boundary. If a previous tick produced a bound, re-emit it so
// the router keeps using the last known good value. Otherwise emit
// UnknownBound=0 so consumers get an explicit "we don't know" signal instead
// of silence.
func (s *StellarHorizonLowerBoundDetector) fallback(reason error) []protocol.LowerBoundData {
	if cached := s.lastBound.Load(); cached > 0 {
		log.Warn().Err(reason).Msgf(
			"horizon upstream '%s' lower-bound detection failed; retaining cached bound=%d",
			s.upstreamId, cached,
		)
		return stellarHorizonBounds(cached)
	}
	log.Warn().Err(reason).Msgf(
		"horizon upstream '%s' lower-bound detection failed and no cache available; emitting UnknownBound",
		s.upstreamId,
	)
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(0, protocol.UnknownBound),
	}
}

func stellarHorizonBounds(bound uint64) []protocol.LowerBoundData {
	bounds := make([]protocol.LowerBoundData, 0, len(stellarHorizonEmittedBoundTypes))
	for _, bt := range stellarHorizonEmittedBoundTypes {
		bounds = append(bounds, protocol.NewLowerBoundDataNow(int64(bound), bt)) //nolint:gosec // ledger sequences are far below int64 max
	}
	return bounds
}

func (s *StellarHorizonLowerBoundDetector) fetchElderLedger(ctx context.Context) (uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET#/", nil, s.chain)

	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return 0, fmt.Errorf("cannot fetch the horizon root document: %w", response.GetError())
	}
	raw := response.ResponseResult()
	if len(raw) == 0 {
		return 0, fmt.Errorf("horizon upstream '%s' returned an empty root document", s.upstreamId)
	}
	var root stellarHorizonRoot
	if err := sonic.Unmarshal(raw, &root); err != nil {
		return 0, fmt.Errorf("horizon upstream '%s' root document unparseable: %w", s.upstreamId, err)
	}
	return root.HistoryElderLedger, nil
}

type stellarHorizonRoot struct {
	HistoryElderLedger uint64 `json:"history_elder_ledger"`
}

var _ lower_bounds.LowerBoundDetector = (*StellarHorizonLowerBoundDetector)(nil)
var _ lower_bounds.DecreasingBoundDetector = (*StellarHorizonLowerBoundDetector)(nil)
