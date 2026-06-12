package lower_bounds

import (
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

const (
	defaultLowerBoundSearchRetryAttempts = 3
	defaultLowerBoundSearchRetryDelay    = 100 * time.Millisecond
)

type LowerBoundProbe func(height int64) (bool, error)

type LowerBoundLatestHeightFetcher func() (int64, error)

type LowerBoundSearchCalculator struct {
	UpstreamId    string
	MainBoundType protocol.LowerBoundType

	allSupportedTypes []protocol.LowerBoundType
	period            time.Duration

	retryAttempts int
	retryDelay    time.Duration

	lastBound atomic.Int64
}

func NewLowerBoundSearchCalculator(
	upstreamId string,
	boundType protocol.LowerBoundType,
	period time.Duration,
) *LowerBoundSearchCalculator {
	return &LowerBoundSearchCalculator{
		UpstreamId:        upstreamId,
		MainBoundType:     boundType,
		period:            period,
		retryAttempts:     defaultLowerBoundSearchRetryAttempts,
		retryDelay:        defaultLowerBoundSearchRetryDelay,
		allSupportedTypes: []protocol.LowerBoundType{boundType},
	}
}

func NewLowerBoundSearchCalculatorWithSupportedTypes(
	upstreamId string,
	boundType protocol.LowerBoundType,
	allSupportedTypes []protocol.LowerBoundType,
	period time.Duration,
) *LowerBoundSearchCalculator {
	return &LowerBoundSearchCalculator{
		UpstreamId:        upstreamId,
		MainBoundType:     boundType,
		period:            period,
		retryAttempts:     defaultLowerBoundSearchRetryAttempts,
		retryDelay:        defaultLowerBoundSearchRetryDelay,
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

	bound, err := c.locateBound(c.lastBound.Load(), latest, probe)
	if err != nil {
		return nil, err
	}
	c.lastBound.Store(bound)

	return c.lowerBoundResults(bound), nil
}

func (c *LowerBoundSearchCalculator) SupportedTypes() []protocol.LowerBoundType {
	return c.allSupportedTypes
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

func (c *LowerBoundSearchCalculator) locateBound(cached, latest int64, probe LowerBoundProbe) (int64, error) {
	if cached > 0 {
		available, err := c.withRetryProbe(cached, probe)
		if err != nil {
			return 0, err
		}
		if available {
			return cached, nil
		}
		return c.binarySearchLower(cached+1, latest, probe)
	}

	available, err := c.withRetryProbe(1, probe)
	if err != nil {
		return 0, err
	}
	if available {
		return 1, nil
	}
	if latest < 2 {
		return 0, fmt.Errorf("upstream '%s' retains no %s data (latest=%d)", c.UpstreamId, c.MainBoundType.String(), latest)
	}
	return c.binarySearchLower(2, latest, probe)
}

func (c *LowerBoundSearchCalculator) binarySearchLower(lo, hi int64, probe LowerBoundProbe) (int64, error) {
	if lo > hi {
		return 0, fmt.Errorf("upstream '%s' empty %s lower-bound range [%d, %d]", c.UpstreamId, c.MainBoundType.String(), lo, hi)
	}

	var searchErr error
	searchRange := int(hi - lo + 1)
	idx := sort.Search(searchRange, func(i int) bool {
		if searchErr != nil {
			return true
		}

		available, err := c.withRetryProbe(lo+int64(i), probe)
		if err != nil {
			searchErr = err
			return true
		}
		return available
	})
	if searchErr != nil {
		return 0, searchErr
	}
	if idx == searchRange {
		return 0, fmt.Errorf("upstream '%s' has no retained %s data in [%d, %d]", c.UpstreamId, c.MainBoundType.String(), lo, hi)
	}
	return lo + int64(idx), nil
}

func (c *LowerBoundSearchCalculator) withRetryLatestHeight(fetchLatestHeight LowerBoundLatestHeightFetcher) (int64, error) {
	executor := failsafe.NewExecutor(createLowerBoundSearchRetryPolicy[int64](c.retryAttempts, c.retryDelay))
	return executor.GetWithExecution(func(exec failsafe.Execution[int64]) (int64, error) {
		return fetchLatestHeight()
	})
}

func (c *LowerBoundSearchCalculator) withRetryProbe(height int64, probe LowerBoundProbe) (bool, error) {
	executor := failsafe.NewExecutor(createLowerBoundSearchRetryPolicy[bool](c.retryAttempts, c.retryDelay))
	return executor.GetWithExecution(func(exec failsafe.Execution[bool]) (bool, error) {
		return probe(height)
	})
}

func createLowerBoundSearchRetryPolicy[T any](attempts int, delay time.Duration) failsafe.Policy[T] {
	if attempts <= 0 {
		attempts = 1
	}
	if delay <= 0 {
		delay = defaultLowerBoundSearchRetryDelay
	}

	retryPolicy := retrypolicy.Builder[T]()
	retryPolicy.WithMaxAttempts(attempts)
	retryPolicy.WithBackoff(delay, delay)
	retryPolicy.HandleIf(func(result T, err error) bool {
		return err != nil
	})
	retryPolicy.ReturnLastFailure()

	return retryPolicy.Build()
}
