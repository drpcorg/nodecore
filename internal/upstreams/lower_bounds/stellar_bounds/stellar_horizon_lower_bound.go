package stellar_bounds

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
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
// root document - Horizon publishes its history boundary directly, one call
// per refresh. On any error the detector returns (nil, err): the processor
// logs it, skips the tick, and the previously cached bound stays in place.
//
// No StateBound: horizon serves ledger/tx history, current state comes from
// captive core with no historical depth.
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
	retryPolicy := retrypolicy.NewBuilder[uint64]().
		WithMaxAttempts(stellarRetryAttempts).
		WithDelay(stellarRetryDelay).
		ReturnLastFailure().
		Build()
	elder, err := failsafe.With(retryPolicy).WithContext(ctx).Get(func() (uint64, error) {
		return s.fetchElderLedger(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("cannot fetch the horizon history boundary for upstream '%s': %w", s.upstreamId, err)
	}
	if elder == 0 {
		// Zero or absent means the node did not report its history
		// boundary, not that the full history is available.
		return nil, errStellarHorizonNoElderLedger
	}

	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(int64(elder), protocol.BlockBound), //nolint:gosec // ledger sequences are far below int64 max
		protocol.NewLowerBoundDataNow(int64(elder), protocol.TxBound),    //nolint:gosec // ledger sequences are far below int64 max
	}, nil
}

func (s *StellarHorizonLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.BlockBound, protocol.TxBound}
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
