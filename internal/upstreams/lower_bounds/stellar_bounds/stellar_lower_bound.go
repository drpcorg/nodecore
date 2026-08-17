package stellar_bounds

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

// stellar-rpc keeps a sliding ledgerRetentionWindow (~7 days at ~5s/ledger),
// so oldestLedger climbs roughly one ledger every 5 seconds; re-poll often to
// keep the bound close to the real retention boundary.
const stellarPeriod = 2 * time.Minute

const (
	stellarRetryAttempts = 3
	stellarRetryDelay    = 500 * time.Millisecond
)

var errStellarNoOldestLedger = errors.New("stellar node reported no oldestLedger")

// StellarLowerBoundDetector reads oldestLedger from getHealth - stellar-rpc
// publishes its retention boundary directly, one RPC per refresh. On any error
// it returns (nil, err): the processor logs it, skips the tick, and the
// previously published bound stays in place.
//
// StateBound only: it is the bound the dRPC dispatcher consults for this
// family. getLedgerEntries serves live state exclusively, so nothing deeper is
// claimed either way.
type StellarLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
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
	retryPolicy := retrypolicy.NewBuilder[*specific_helpers.StellarHealth]().
		WithMaxAttempts(stellarRetryAttempts).
		WithDelay(stellarRetryDelay).
		ReturnLastFailure().
		Build()
	health, err := failsafe.With(retryPolicy).WithContext(ctx).Get(func() (*specific_helpers.StellarHealth, error) {
		return s.fetchHealth(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("cannot fetch stellar health for upstream '%s': %w", s.upstreamId, err)
	}
	if health.OldestLedger == 0 {
		// Zero or absent means the node did not report its retention
		// boundary, not that the full history is available.
		return nil, errStellarNoOldestLedger
	}

	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(int64(health.OldestLedger), protocol.StateBound), //nolint:gosec // ledger sequences are far below int64 max
	}, nil
}

func (s *StellarLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.StateBound}
}

func (s *StellarLowerBoundDetector) Period() time.Duration {
	return stellarPeriod
}

func (s *StellarLowerBoundDetector) fetchHealth(ctx context.Context) (*specific_helpers.StellarHealth, error) {
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	return specific_helpers.FetchStellarHealth(ctx, s.connector, s.chain)
}

var _ lower_bounds.LowerBoundDetector = (*StellarLowerBoundDetector)(nil)
