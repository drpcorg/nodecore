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

// stellar-rpc keeps a sliding ledgerRetentionWindow (~7 days at ~5s/ledger),
// so oldestLedger climbs roughly one ledger every 5 seconds; re-poll often to
// keep the bound close to the real retention boundary.
const stellarPeriod = 2 * time.Minute

var errStellarNoOldestLedger = errors.New("stellar node reported no oldestLedger")

// No StateBound: getLedgerEntries serves live state only, there is no
// historical state on stellar-rpc at all.
var stellarEmittedBoundTypes = []protocol.LowerBoundType{
	protocol.BlockBound,
	protocol.TxBound,
}

type StellarLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration

	lastBound atomic.Uint64
}

func NewStellarLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *StellarLowerBoundDetector {
	return &StellarLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *StellarLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	health, err := s.fetchHealth(ctx)
	if err != nil {
		return s.fallback(fmt.Errorf("cannot fetch node health: %w", err)), nil
	}

	bound := health.OldestLedger
	if bound == 0 {
		// Zero or absent means the node did not report its retention
		// boundary, not that the full history is available.
		return s.fallback(errStellarNoOldestLedger), nil
	}
	s.lastBound.Store(bound)

	return stellarBounds(bound), nil
}

func (s *StellarLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return append(append([]protocol.LowerBoundType{}, stellarEmittedBoundTypes...), protocol.UnknownBound)
}

func (s *StellarLowerBoundDetector) Period() time.Duration {
	return stellarPeriod
}

// fallback decides what to publish when the calculation cannot complete.
// If a previous tick produced a bound, re-emit it so the router keeps using
// the last known good value. Otherwise emit UnknownBound=0 so consumers get
// an explicit "we don't know" signal instead of silence.
func (s *StellarLowerBoundDetector) fallback(reason error) []protocol.LowerBoundData {
	if cached := s.lastBound.Load(); cached > 0 {
		log.Warn().Err(reason).Msgf(
			"stellar upstream '%s' lower-bound calculation failed; retaining cached bound=%d",
			s.upstreamId, cached,
		)
		return stellarBounds(cached)
	}
	log.Warn().Err(reason).Msgf(
		"stellar upstream '%s' lower-bound calculation failed and no cache available; emitting UnknownBound",
		s.upstreamId,
	)
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(0, protocol.UnknownBound),
	}
}

func stellarBounds(bound uint64) []protocol.LowerBoundData {
	bounds := make([]protocol.LowerBoundData, 0, len(stellarEmittedBoundTypes))
	for _, bt := range stellarEmittedBoundTypes {
		bounds = append(bounds, protocol.NewLowerBoundDataNow(int64(bound), bt)) //nolint:gosec // ledger sequences are far below int64 max
	}
	return bounds
}

func (s *StellarLowerBoundDetector) fetchHealth(ctx context.Context) (*stellarHealth, error) {
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getHealth", map[string]any{}, s.chain)
	if err != nil {
		return nil, err
	}

	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	raw := response.ResponseResult()
	if len(raw) == 0 {
		return nil, fmt.Errorf("stellar upstream '%s' getHealth returned an empty body", s.upstreamId)
	}
	var health stellarHealth
	if err := sonic.Unmarshal(raw, &health); err != nil {
		return nil, fmt.Errorf("stellar upstream '%s' getHealth unparseable: %w", s.upstreamId, err)
	}
	return &health, nil
}

type stellarHealth struct {
	OldestLedger uint64 `json:"oldestLedger"`
}

var _ lower_bounds.LowerBoundDetector = (*StellarLowerBoundDetector)(nil)
