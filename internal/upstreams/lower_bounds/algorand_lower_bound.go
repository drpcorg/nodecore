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
)

const algorandPeriod = 5 * time.Minute

var errAlgorandNoLatestRound = errors.New("algorand node returned no last-round")

// AlgorandLowerBoundDetector reports the lowest round that an algod upstream
// still retains.
//
// algod does not expose a lower-bound field directly:
//   - `/v2/status` only carries `last-round` (the head).
//   - `/v2/ledger/sync` is an admin pin used during catchpoint catchup; once
//     the operator unsets it the endpoint returns 400, so it cannot be used as
//     a general source of truth.
//
// The cheapest reliable signal is therefore a probe of `/v2/blocks/{round}`:
// algod answers 200 when the round is retained and 404 (with a JSON body)
// once it has been pruned. Header-only mode keeps each probe response small.
//
// The detector amortises the cost as follows:
//   - First run: binary-search [1, last_round] using `/v2/blocks/{round}` as
//     the predicate. O(log latest_round) probes, ~30 calls for a 50M-round
//     mainnet ledger - paid once at startup.
//   - Subsequent runs: probe the cached lower bound. If still retained,
//     return immediately (one call). If pruned, binary-search forward in
//     [cached+1, last_round] - the prune boundary moves forward only.
//
// On any error path we bubble the error up so [BaseLowerBoundProcessor] skips
// the tick and keeps the previously published bound. Synthesising STATE=1
// here would falsely advertise full archival history to the router.
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
		return nil, fmt.Errorf("algorand lower bound: cannot fetch latest round: %w", err)
	}
	if latest == 0 {
		return nil, errAlgorandNoLatestRound
	}

	cached := a.lastBound.Load()
	bound, err := a.locateBound(cached, latest)
	if err != nil {
		return nil, err
	}
	a.lastBound.Store(bound)

	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(bound, protocol.StateBound),
	}, nil
}

func (a *AlgorandLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.StateBound}
}

func (a *AlgorandLowerBoundDetector) Period() time.Duration {
	return algorandPeriod
}

// locateBound returns the smallest round in [1, latest] for which `/v2/blocks`
// returns a non-404 response. When `cached > 0` we first verify it is still
// retained and otherwise narrow the search to (cached, latest].
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
	// Cold start: probe round 1 first to short-circuit archival nodes.
	available, err := a.hasBlock(1)
	if err != nil {
		return 0, err
	}
	if available {
		return 1, nil
	}
	return a.binarySearchLower(2, latest)
}

// binarySearchLower returns the smallest round in [lo, hi] whose block is
// retained. Assumes the predicate is monotonic (once a round is retained,
// all higher rounds are too) which holds for algod's prune behaviour.
func (a *AlgorandLowerBoundDetector) binarySearchLower(lo, hi int64) (int64, error) {
	if lo > hi {
		return hi, nil
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
		// Nothing in [lo, hi] is retained right now - give up rather than
		// advertise a bogus bound. The next tick will retry.
		return 0, fmt.Errorf("algorand upstream '%s' has no retained block in [%d, %d]", a.upstreamId, lo, hi)
	}
	return result, nil
}

// hasBlock reports whether `GET /v2/blocks/{round}?header-only=true` returns
// a successful response. 404 / "block not found" / "not available" are
// treated as a clean "missing" signal; other errors are surfaced.
func (a *AlgorandLowerBoundDetector) hasBlock(round int64) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	path := fmt.Sprintf("/v2/blocks/%d?header-only=true", round)
	request := protocol.NewInternalUpstreamRestRequest("GET", path, a.chain)

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		// Distinguish "round was pruned" (a 404 / well-known message) from
		// "the upstream is misbehaving" (transport error, 5xx). The former
		// must not propagate as an error or the binary search collapses.
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
		return false, nil
	}
	// algod sometimes returns 200 with an error envelope. Probe for a `block`
	// key to distinguish a real header from an error body.
	var probe map[string]any
	if err := sonic.Unmarshal(raw, &probe); err != nil {
		// Unparseable payload - treat as transient by surfacing the error.
		return false, fmt.Errorf("algorand upstream '%s' /v2/blocks/%d unparseable: %w", a.upstreamId, round, err)
	}
	if _, ok := probe["block"]; ok {
		return true, nil
	}
	if msg, ok := probe["message"].(string); ok && isNotFoundMessage(msg) {
		return false, nil
	}
	if _, ok := probe["cert"]; ok {
		return true, nil
	}
	return false, nil
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

// algod 404 phrasing varies between releases; keep this list narrow but
// inclusive of the strings that are observed in the wild against
// /v2/blocks/{round}.
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
