package ripple_bounds

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// A non-archival rippled keeps a sliding history window (online deletion), so
// the low edge of complete_ledgers climbs roughly one ledger per 4s; an
// archival node backfills backwards instead. Either way the bound moves fast
// enough that a short re-poll period is warranted.
const ripplePeriod = 2 * time.Minute

var rippleEmittedBoundTypes = []protocol.LowerBoundType{
	protocol.StateBound,
	protocol.BlockBound,
}

type RippleLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration

	lastBound atomic.Int64
}

func NewRippleLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *RippleLowerBoundDetector {
	return &RippleLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (r *RippleLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	state, err := r.fetchServerState(ctx)
	if err != nil {
		return r.fallback(fmt.Errorf("cannot fetch server_state: %w", err)), nil
	}

	bound, err := parseCompleteLedgers(state.State.CompleteLedgers)
	if err != nil {
		return r.fallback(err), nil
	}
	r.lastBound.Store(bound)

	return rippleBounds(bound), nil
}

func (r *RippleLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return append(append([]protocol.LowerBoundType{}, rippleEmittedBoundTypes...), protocol.UnknownBound)
}

func (r *RippleLowerBoundDetector) Period() time.Duration {
	return ripplePeriod
}

// AllowsBoundDecrease opts this detector out of the processor's monotonic
// filter: an archival rippled (online_delete=0 + full ledger_history)
// backfills history backwards, so the lower bound legitimately decreases
// over time toward genesis.
func (r *RippleLowerBoundDetector) AllowsBoundDecrease() bool {
	return true
}

// fallback decides what to publish when the calculation cannot complete.
// If a previous tick produced a bound, re-emit it so the router keeps using
// the last known good value. Otherwise emit UnknownBound=0 so consumers get
// an explicit "we don't know" signal instead of silence.
func (r *RippleLowerBoundDetector) fallback(reason error) []protocol.LowerBoundData {
	if cached := r.lastBound.Load(); cached > 0 {
		log.Warn().Err(reason).Msgf(
			"ripple upstream '%s' lower-bound calculation failed; retaining cached bound=%d",
			r.upstreamId, cached,
		)
		return rippleBounds(cached)
	}
	log.Warn().Err(reason).Msgf(
		"ripple upstream '%s' lower-bound calculation failed and no cache available; emitting UnknownBound",
		r.upstreamId,
	)
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(0, protocol.UnknownBound),
	}
}

func rippleBounds(bound int64) []protocol.LowerBoundData {
	bounds := make([]protocol.LowerBoundData, 0, len(rippleEmittedBoundTypes))
	for _, bt := range rippleEmittedBoundTypes {
		bounds = append(bounds, protocol.NewLowerBoundDataNow(bound, bt))
	}
	return bounds
}

// parseCompleteLedgers extracts the lower bound from rippled's
// complete_ledgers string. The value is either the literal "empty" (a node
// with no history yet), a single range "a-b", or a comma-separated list of
// disjoint ranges "a-b,c-d,...". For disjoint ranges the start of the LAST
// range is used: that is the contiguous window ending at the tip, i.e. the
// oldest ledger from which every request up to the head can be served.
func parseCompleteLedgers(completeLedgers string) (int64, error) {
	trimmed := strings.TrimSpace(completeLedgers)
	if trimmed == "" || trimmed == "empty" {
		return 0, fmt.Errorf("ripple node reported no complete ledgers: %q", completeLedgers)
	}

	ranges := strings.Split(trimmed, ",")
	lastRange := strings.TrimSpace(ranges[len(ranges)-1])

	// a range is either "start-end" or a single ledger index
	start, _, _ := strings.Cut(lastRange, "-")
	bound, err := strconv.ParseInt(strings.TrimSpace(start), 10, 64)
	if err != nil || bound <= 0 {
		return 0, fmt.Errorf("ripple node reported malformed complete_ledgers: %q", completeLedgers)
	}
	return bound, nil
}

func (r *RippleLowerBoundDetector) fetchServerState(ctx context.Context) (*rippleServerState, error) {
	ctx, cancel := context.WithTimeout(ctx, r.internalTimeout)
	defer cancel()

	// rippled REQUIRES params to be an array with exactly one object
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("server_state", []any{map[string]any{}}, r.chain)
	if err != nil {
		return nil, err
	}

	response := r.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	raw := response.ResponseResult()
	if len(raw) == 0 {
		return nil, fmt.Errorf("ripple upstream '%s' server_state returned an empty body", r.upstreamId)
	}
	var state rippleServerState
	if err := sonic.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("ripple upstream '%s' server_state unparseable: %w", r.upstreamId, err)
	}
	return &state, nil
}

type rippleServerState struct {
	State rippleState `json:"state"`
}

type rippleState struct {
	CompleteLedgers string `json:"complete_ledgers"`
}

var _ lower_bounds.LowerBoundDetector = (*RippleLowerBoundDetector)(nil)
var _ lower_bounds.DecreasingBoundDetector = (*RippleLowerBoundDetector)(nil)
