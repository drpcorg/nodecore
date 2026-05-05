package lower_bounds

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const algorandPeriod = 5 * time.Minute

var errAlgorandNoLatestRound = errors.New("algorand node returned no last-round")

type AlgorandLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration

	lastBound atomic.Int64
}

func NewAlgorandLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *AlgorandLowerBoundDetector {
	return &AlgorandLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (a *AlgorandLowerBoundDetector) DetectLowerBound() ([]protocol.LowerBoundData, error) {
	latest, err := a.fetchLatestRound()
	if err != nil {
		return a.fallback(fmt.Errorf("cannot fetch latest round: %w", err)), nil
	}
	if latest == 0 {
		return a.fallback(errAlgorandNoLatestRound), nil
	}

	cached := a.lastBound.Load()
	bound, err := a.locateBound(cached, latest)
	if err != nil {
		return a.fallback(err), nil
	}
	a.lastBound.Store(bound)

	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(bound, protocol.StateBound),
	}, nil
}

func (a *AlgorandLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.StateBound, protocol.UnknownBound}
}

func (a *AlgorandLowerBoundDetector) Period() time.Duration {
	return algorandPeriod
}

// fallback decides what to publish when the calculation cannot complete.
// If a previous tick produced a bound, re-emit it so the router keeps using
// the last known good value. Otherwise emit UnknownBound=0 so consumers get
// an explicit "we don't know" signal instead of silence.
func (a *AlgorandLowerBoundDetector) fallback(reason error) []protocol.LowerBoundData {
	if cached := a.lastBound.Load(); cached > 0 {
		log.Warn().Err(reason).Msgf(
			"algorand upstream '%s' lower-bound calculation failed; retaining cached STATE=%d",
			a.upstreamId, cached,
		)
		return []protocol.LowerBoundData{
			protocol.NewLowerBoundDataNow(cached, protocol.StateBound),
		}
	}
	log.Warn().Err(reason).Msgf(
		"algorand upstream '%s' lower-bound calculation failed and no cache available; emitting UnknownBound",
		a.upstreamId,
	)
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(0, protocol.UnknownBound),
	}
}

func (a *AlgorandLowerBoundDetector) locateBound(cached, latest int64) (int64, error) {
	if cached > 0 {
		available, err := a.hasBlock(cached)
		if err != nil {
			return 0, err
		}
		if available {
			return cached, nil
		}
		return a.binarySearchLower(cached+1, latest)
	}
	available, err := a.hasBlock(1)
	if err != nil {
		return 0, err
	}
	if available {
		return 1, nil
	}
	if latest < 2 {
		return 0, fmt.Errorf("algorand upstream '%s' retains no blocks (last-round=%d)", a.upstreamId, latest)
	}
	return a.binarySearchLower(2, latest)
}

func (a *AlgorandLowerBoundDetector) binarySearchLower(lo, hi int64) (int64, error) {
	if lo > hi {
		return 0, fmt.Errorf("algorand upstream '%s' empty search range [%d, %d]", a.upstreamId, lo, hi)
	}
	left, right := lo, hi
	var result int64
	for left <= right {
		mid := left + (right-left)/2
		available, err := a.hasBlock(mid)
		if err != nil {
			return 0, err
		}
		if available {
			result = mid
			right = mid - 1
		} else {
			left = mid + 1
		}
	}
	if result == 0 {
		return 0, fmt.Errorf("algorand upstream '%s' has no retained block in [%d, %d]", a.upstreamId, lo, hi)
	}
	return result, nil
}

func (a *AlgorandLowerBoundDetector) hasBlock(round int64) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	path := fmt.Sprintf("/v2/blocks/%d/hash", round)
	request := protocol.NewInternalUpstreamRestRequest("GET", path, a.chain)

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		respErr := response.GetError()
		if respErr != nil {
			if response.ResponseCode() == 404 || isNotFoundMessage(respErr.Message) {
				return false, nil
			}
			return false, respErr
		}
	}
	raw := response.ResponseResult()
	if len(raw) == 0 {
		return false, fmt.Errorf("algorand upstream '%s' /v2/blocks/%d/hash returned empty body", a.upstreamId, round)
	}
	var probe struct {
		BlockHash string `json:"blockHash"`
		Message   string `json:"message"`
	}
	if err := sonic.Unmarshal(raw, &probe); err != nil {
		return false, fmt.Errorf("algorand upstream '%s' /v2/blocks/%d/hash unparseable: %w", a.upstreamId, round, err)
	}
	if probe.BlockHash != "" {
		return true, nil
	}
	if isNotFoundMessage(probe.Message) {
		return false, nil
	}
	if probe.Message != "" {
		return false, fmt.Errorf("algorand upstream '%s' /v2/blocks/%d/hash unexpected error: %s", a.upstreamId, round, probe.Message)
	}
	return false, fmt.Errorf("algorand upstream '%s' /v2/blocks/%d/hash returned an unrecognised body", a.upstreamId, round)
}

func (a *AlgorandLowerBoundDetector) fetchLatestRound() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET", "/v2/status", a.chain)

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return 0, response.GetError()
	}
	var status algorandStatusEnvelope
	if err := sonic.Unmarshal(response.ResponseResult(), &status); err != nil {
		return 0, err
	}
	return int64(status.LastRound), nil
}

func isNotFoundMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	for _, hint := range notFoundHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

var notFoundHints = []string{
	"block not found",
	"not available",
	"does not have entry",
	"failed to retrieve information",
	"no information found",
}

type algorandStatusEnvelope struct {
	LastRound uint64 `json:"last-round"`
}

var _ LowerBoundDetector = (*AlgorandLowerBoundDetector)(nil)
