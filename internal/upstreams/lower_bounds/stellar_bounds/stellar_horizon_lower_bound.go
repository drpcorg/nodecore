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

// Horizon's retention reaper slides history_elder_ledger up continuously on
// retention-limited deployments; re-poll often to keep the bound close to the
// real history boundary.
const stellarHorizonPeriod = 2 * time.Minute

var errStellarHorizonNoElderLedger = errors.New("horizon reported no history_elder_ledger")

// StellarHorizonLowerBoundDetector reads history_elder_ledger from Horizon's
// root document, one call per refresh. On any error it returns (nil, err): the
// processor logs it, skips the tick, and the previously published bound stays.
//
// Note the bound can legitimately move DOWN when `horizon db reingest range`
// backfills older ledgers. The processor's monotonic filter keeps the shallower
// value, so nodecore under-claims history until a restart - conservative by
// design (see the design doc); revisit if backfills become routine.
type StellarHorizonLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
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
	retryPolicy := retrypolicy.NewBuilder[*specific_helpers.StellarHorizonRoot]().
		WithMaxAttempts(stellarRetryAttempts).
		WithDelay(stellarRetryDelay).
		ReturnLastFailure().
		Build()
	root, err := failsafe.With(retryPolicy).WithContext(ctx).Get(func() (*specific_helpers.StellarHorizonRoot, error) {
		return s.fetchRoot(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("cannot fetch the horizon history boundary for upstream '%s': %w", s.upstreamId, err)
	}
	if root.HistoryElderLedger == 0 {
		// Zero or absent means the node did not report its history boundary,
		// not that the full history is available.
		return nil, errStellarHorizonNoElderLedger
	}

	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(int64(root.HistoryElderLedger), protocol.StateBound), //nolint:gosec // ledger sequences are far below int64 max
	}, nil
}

func (s *StellarHorizonLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.StateBound}
}

func (s *StellarHorizonLowerBoundDetector) Period() time.Duration {
	return stellarHorizonPeriod
}

func (s *StellarHorizonLowerBoundDetector) fetchRoot(ctx context.Context) (*specific_helpers.StellarHorizonRoot, error) {
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	return specific_helpers.FetchStellarHorizonRoot(ctx, s.connector, s.chain)
}

var _ lower_bounds.LowerBoundDetector = (*StellarHorizonLowerBoundDetector)(nil)
