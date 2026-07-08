package lower_bounds

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

const (
	defaultLowerBoundSearchRetryAttempts  = 30
	defaultLowerBoundSearchRetryBaseDelay = 1 * time.Second
	defaultLowerBoundSearchRetryMaxDelay  = 1 * time.Minute
)

type LowerBoundProbe func(height int64) (bool, error)

type LowerBoundLatestHeightFetcher func() (int64, error)

type boundRange struct {
	left    int64
	right   int64
	current int64
	found   bool
}

type LowerBoundSearchCalculator struct {
	UpstreamId    string
	MainBoundType protocol.LowerBoundType

	allSupportedTypes []protocol.LowerBoundType
	period            time.Duration

	maxOffset int

	retryAttempts  int
	retryBaseDelay time.Duration
	retryMaxDelay  time.Duration

	lastBound atomic.Int64
}

func NewLowerBoundSearchCalculator(
	upstreamId string,
	boundType protocol.LowerBoundType,
	period time.Duration,
) *LowerBoundSearchCalculator {
	return NewLowerBoundSearchCalculatorWithOffset(upstreamId, boundType, []protocol.LowerBoundType{boundType}, period, 0)
}

func NewLowerBoundSearchCalculatorWithSupportedTypes(
	upstreamId string,
	boundType protocol.LowerBoundType,
	allSupportedTypes []protocol.LowerBoundType,
	period time.Duration,
) *LowerBoundSearchCalculator {
	return NewLowerBoundSearchCalculatorWithOffset(upstreamId, boundType, allSupportedTypes, period, 0)
}

func NewLowerBoundSearchCalculatorWithOffset(
	upstreamId string,
	boundType protocol.LowerBoundType,
	allSupportedTypes []protocol.LowerBoundType,
	period time.Duration,
	maxOffset int,
) *LowerBoundSearchCalculator {
	return &LowerBoundSearchCalculator{
		UpstreamId:        upstreamId,
		MainBoundType:     boundType,
		period:            period,
		maxOffset:         maxOffset,
		retryAttempts:     defaultLowerBoundSearchRetryAttempts,
		retryBaseDelay:    defaultLowerBoundSearchRetryBaseDelay,
		retryMaxDelay:     defaultLowerBoundSearchRetryMaxDelay,
		allSupportedTypes: allSupportedTypes,
	}
}

func (c *LowerBoundSearchCalculator) DetectLowerBound(
	fetchLatestHeight LowerBoundLatestHeightFetcher,
	probe LowerBoundProbe,
) ([]protocol.LowerBoundData, error) {
	latest, err := c.withRetryLatestHeight(fetchLatestHeight)
	if err != nil {
		return nil, fmt.Errorf("cannot fetch latest height for upstream '%s': %w", c.UpstreamId, err)
	}
	if latest <= 0 {
		return nil, fmt.Errorf("upstream '%s' returned non-positive latest height %d", c.UpstreamId, latest)
	}

	hasData := func(height int64) bool {
		available, err := c.withRetryProbe(height, probe)
		return err == nil && available
	}

	cached := c.lastBound.Load()

	var bound int64
	if c.maxOffset > 0 {
		bound, err = c.detectWithOffset(cached, latest, hasData)
	} else {
		bound, err = c.detectPlain(cached, latest, hasData)
	}
	if err != nil {
		return nil, err
	}
	c.lastBound.Store(bound)

	return c.lowerBoundResults(bound), nil
}

func (c *LowerBoundSearchCalculator) SupportedTypes() []protocol.LowerBoundType {
	return c.allSupportedTypes
}

// SetSearchRetryPolicy overrides the retry behavior applied to each probe and latest-height fetch.
// Production relies on the defaults; this exists mainly to let tests run with negligible backoff.
func (c *LowerBoundSearchCalculator) SetSearchRetryPolicy(attempts int, baseDelay, maxDelay time.Duration) {
	c.retryAttempts = attempts
	c.retryBaseDelay = baseDelay
	c.retryMaxDelay = maxDelay
}

// LowerBoundResults builds the fanned-out result set for an externally determined
// bound (e.g. a gold-bound short-circuit), reusing the same per-supported-type
// expansion the search uses.
func (c *LowerBoundSearchCalculator) LowerBoundResults(bound int64) []protocol.LowerBoundData {
	return c.lowerBoundResults(bound)
}

func (c *LowerBoundSearchCalculator) lowerBoundResults(bound int64) []protocol.LowerBoundData {
	results := make([]protocol.LowerBoundData, 0, len(c.allSupportedTypes))
	if len(c.allSupportedTypes) == 0 {
		return []protocol.LowerBoundData{protocol.NewLowerBoundDataNow(bound, c.MainBoundType)}
	}
	for _, boundType := range c.allSupportedTypes {
		results = append(results, protocol.NewLowerBoundDataNow(bound, boundType))
	}
	return results
}

func (c *LowerBoundSearchCalculator) Period() time.Duration {
	return c.period
}

func (c *LowerBoundSearchCalculator) initialRange(cached, latest int64) boundRange {
	left := int64(0)
	if cached > 0 {
		left = cached
	}
	return boundRange{left: left, right: latest, current: 0}
}

func (c *LowerBoundSearchCalculator) detectPlain(cached, latest int64, hasData func(int64) bool) (int64, error) {
	state := c.initialRange(cached, latest)
	for !state.found {
		if state.left > state.right {
			converged, err := c.converge(state, hasData)
			if err != nil {
				return 0, err
			}
			state = converged
			continue
		}

		middle := state.left + (state.right-state.left)/2
		if hasData(middle) {
			state = boundRange{left: state.left, right: middle - 1, current: middle}
		} else {
			state = boundRange{left: middle + 1, right: state.right, current: state.current}
		}
	}
	return state.current, nil
}

// detectWithOffset ports RecursiveLowerBound.recursiveDetectLowerBoundWithOffset: it first
// re-checks the cached bound, then binary-searches with shiftLeftAndSearch to tolerate sporadic
// missing blocks below a probed middle.
func (c *LowerBoundSearchCalculator) detectWithOffset(cached, latest int64, hasData func(int64) bool) (int64, error) {
	// First, try to confirm the cached bound to avoid a full re-search.
	if cached > 0 && hasData(cached) {
		return cached, nil
	}

	visited := make(map[int64]struct{})
	state := c.initialRange(cached, latest)
	for !state.found {
		if state.left > state.right {
			converged, err := c.converge(state, hasData)
			if err != nil {
				return 0, err
			}
			state = converged
			continue
		}

		middle := state.left + (state.right-state.left)/2
		if hasData(middle) {
			state = boundRange{left: state.left, right: middle - 1, current: middle}
		} else if middle < 0 {
			state = boundRange{left: middle + 1, right: state.right, current: state.current}
		} else {
			state = c.shiftLeftAndSearch(state, middle, visited, hasData)
		}
	}
	return state.current, nil
}

// converge handles the left > right terminal step: it re-probes the best candidate (coercing the
// genesis floor to 1) and, if that fails on a non-trivial chain where nothing was ever confirmed,
// reports failure so the processor keeps the previously cached bound instead of a bogus 1.
func (c *LowerBoundSearchCalculator) converge(state boundRange, hasData func(int64) bool) (boundRange, error) {
	current := state.current
	if current == 0 {
		current = 1
	}
	if hasData(current) {
		return boundRange{current: current, found: true}, nil
	}
	if current == 1 && state.right > 10 {
		return boundRange{}, fmt.Errorf("upstream '%s' could not detect %s lower bound", c.UpstreamId, c.MainBoundType.String())
	}
	return boundRange{current: current, found: true}, nil
}

// shiftLeftAndSearch ports RecursiveLowerBound.shiftLeftAndSearch: starting just below a no-data
// middle, it scans downward up to maxOffset blocks looking for nearby data. Finding data narrows
// the window left to that block; exhausting the window (or hitting an already-visited block) gives
// up and moves the main search higher. The returned range has found=false so the caller continues.
func (c *LowerBoundSearchCalculator) shiftLeftAndSearch(
	currentData boundRange,
	currentMiddle int64,
	visited map[int64]struct{},
	hasData func(int64) bool,
) boundRange {
	moveRight := boundRange{left: currentMiddle + 1, right: currentData.right, current: currentData.current}
	count := 0
	block := currentMiddle - 1
	for {
		if _, seen := visited[block]; seen || block < 0 {
			return moveRight
		}
		if hasData(block) {
			return boundRange{left: currentData.left, right: block - 1, current: block}
		}
		count++
		if count > c.maxOffset {
			return moveRight
		}
		visited[block] = struct{}{}
		block--
	}
}

func (c *LowerBoundSearchCalculator) withRetryLatestHeight(fetchLatestHeight LowerBoundLatestHeightFetcher) (int64, error) {
	executor := failsafe.NewExecutor(c.createRetryPolicy())
	return executor.GetWithExecution(func(exec failsafe.Execution[int64]) (int64, error) {
		return fetchLatestHeight()
	})
}

func (c *LowerBoundSearchCalculator) withRetryProbe(height int64, probe LowerBoundProbe) (bool, error) {
	executor := failsafe.NewExecutor(createLowerBoundSearchRetryPolicy[bool](c.retryAttempts, c.retryBaseDelay, c.retryMaxDelay))
	return executor.GetWithExecution(func(exec failsafe.Execution[bool]) (bool, error) {
		return probe(height)
	})
}

func (c *LowerBoundSearchCalculator) createRetryPolicy() failsafe.Policy[int64] {
	return createLowerBoundSearchRetryPolicy[int64](c.retryAttempts, c.retryBaseDelay, c.retryMaxDelay)
}

func createLowerBoundSearchRetryPolicy[T any](attempts int, baseDelay, maxDelay time.Duration) failsafe.Policy[T] {
	if attempts <= 0 {
		attempts = 1
	}
	if baseDelay <= 0 {
		baseDelay = defaultLowerBoundSearchRetryBaseDelay
	}
	if maxDelay < baseDelay {
		maxDelay = baseDelay
	}

	retryPolicy := retrypolicy.Builder[T]()
	retryPolicy.WithMaxAttempts(attempts)
	retryPolicy.WithBackoff(baseDelay, maxDelay)
	retryPolicy.HandleIf(func(result T, err error) bool {
		return err != nil
	})
	retryPolicy.ReturnLastFailure()

	return retryPolicy.Build()
}
