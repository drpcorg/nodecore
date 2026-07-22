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
// publishes its retention boundary directly, one RPC per refresh. On any
// error the detector returns (nil, err): the processor logs it, skips the
// tick, and the previously cached bound stays in place.
//
// No StateBound: getLedgerEntries serves live state only, there is no
// historical state on stellar-rpc at all.
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
	retryPolicy := retrypolicy.NewBuilder[*stellarHealth]().
		WithMaxAttempts(stellarRetryAttempts).
		WithDelay(stellarRetryDelay).
		ReturnLastFailure().
		Build()
	health, err := failsafe.With(retryPolicy).WithContext(ctx).Get(func() (*stellarHealth, error) {
		return s.fetchHealth(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("cannot fetch stellar health for upstream '%s': %w", s.upstreamId, err)
	}

	bound := health.OldestLedger
	if bound == 0 {
		// Zero or absent means the node did not report its retention
		// boundary, not that the full history is available.
		return nil, errStellarNoOldestLedger
	}

	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(int64(bound), protocol.BlockBound), //nolint:gosec // ledger sequences are far below int64 max
		protocol.NewLowerBoundDataNow(int64(bound), protocol.TxBound),    //nolint:gosec // ledger sequences are far below int64 max
	}, nil
}

func (s *StellarLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.BlockBound, protocol.TxBound}
}

func (s *StellarLowerBoundDetector) Period() time.Duration {
	return stellarPeriod
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
