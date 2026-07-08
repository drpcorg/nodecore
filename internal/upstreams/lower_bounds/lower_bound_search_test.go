package lower_bounds_test

import (
	"errors"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastRetries makes the retry backoff negligible so error-path tests stay quick.
func fastRetries(c *lower_bounds.LowerBoundSearchCalculator) *lower_bounds.LowerBoundSearchCalculator {
	c.SetSearchRetryPolicy(3, time.Millisecond, time.Millisecond)
	return c
}

func plainCalculator() *lower_bounds.LowerBoundSearchCalculator {
	return fastRetries(lower_bounds.NewLowerBoundSearchCalculator("up-1", protocol.BlockBound, time.Minute))
}

func offsetCalculator(maxOffset int) *lower_bounds.LowerBoundSearchCalculator {
	return fastRetries(lower_bounds.NewLowerBoundSearchCalculatorWithOffset(
		"up-1", protocol.BlockBound, []protocol.LowerBoundType{protocol.BlockBound}, time.Minute, maxOffset,
	))
}

func TestLowerBoundSearchBinarySearchesEarliestAvailable(t *testing.T) {
	calculator := plainCalculator()
	calls := make([]int64, 0)

	result, err := calculator.DetectLowerBound(
		func() (int64, error) { return 8, nil },
		func(height int64) (bool, error) {
			calls = append(calls, height)
			return height >= 4, nil
		},
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.BlockBound, result[0].Type)
	assert.Equal(t, int64(4), result[0].Bound)
	// No block-1 short-circuit: the search runs from the midpoint over [0, 8].
	assert.Equal(t, []int64{4, 1, 2, 3, 4}, calls)
}

// The reported bug: genesis (block 1) is retained but there is a hole above it. The search must
// converge on the real upper boundary, not trust block 1.
func TestLowerBoundSearchIgnoresGenesisWhenHoleFollows(t *testing.T) {
	calculator := plainCalculator()
	calls := make([]int64, 0)

	result, err := calculator.DetectLowerBound(
		func() (int64, error) { return 10, nil },
		func(height int64) (bool, error) {
			calls = append(calls, height)
			// data at genesis and from 6 onward, hole in 2..5
			return height == 1 || height >= 6, nil
		},
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(6), result[0].Bound)
	assert.NotContains(t, calls, int64(1)) // never even needs to probe block 1
}

// A transient error that survives all retries is treated as "no data, search higher" rather than
// aborting the whole detection.
func TestLowerBoundSearchTreatsPersistentErrorAsNoData(t *testing.T) {
	calculator := plainCalculator()

	result, err := calculator.DetectLowerBound(
		func() (int64, error) { return 8, nil },
		func(height int64) (bool, error) {
			if height < 4 {
				return false, errors.New("boom")
			}
			return true, nil
		},
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(4), result[0].Bound)
}

func TestLowerBoundSearchRetriesTransientErrorThenSucceeds(t *testing.T) {
	calculator := plainCalculator()
	attempts := 0

	result, err := calculator.DetectLowerBound(
		func() (int64, error) { return 1, nil },
		func(height int64) (bool, error) {
			attempts++
			if attempts == 1 {
				return false, errors.New("temporary")
			}
			return true, nil
		},
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(1), result[0].Bound)
}

// Convergence guard: on a non-trivial chain where nothing is ever confirmed, detection fails so
// the caller keeps the cached bound instead of a bogus 1.
func TestLowerBoundSearchFailsWhenNothingRetainedOnTallChain(t *testing.T) {
	calculator := plainCalculator()

	result, err := calculator.DetectLowerBound(
		func() (int64, error) { return 100, nil },
		func(height int64) (bool, error) { return false, nil },
	)

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestLowerBoundSearchReturnsOneOnTinyChain(t *testing.T) {
	calculator := plainCalculator()

	// latest is small (<=10), so the convergence guard does not fire and block 1 is reported.
	result, err := calculator.DetectLowerBound(
		func() (int64, error) { return 1, nil },
		func(height int64) (bool, error) { return true, nil },
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(1), result[0].Bound)
}

// Plain variant: a previously found bound is confirmed with a single probe (no full re-search).
func TestLowerBoundSearchPlainReusesCachedBound(t *testing.T) {
	calculator := plainCalculator()
	calls := make([]int64, 0)
	probe := func(height int64) (bool, error) {
		calls = append(calls, height)
		return height >= 5, nil
	}

	first, err := calculator.DetectLowerBound(func() (int64, error) { return 8, nil }, probe)
	require.NoError(t, err)
	assert.Equal(t, int64(5), first[0].Bound)

	callsAfterFirst := len(calls)
	second, err := calculator.DetectLowerBound(func() (int64, error) { return 9, nil }, probe)
	require.NoError(t, err)
	assert.Equal(t, int64(5), second[0].Bound)
	assert.Equal(t, []int64{5}, calls[callsAfterFirst:])
}

// Offset variant: a previously found bound is confirmed with a single probe.
func TestLowerBoundSearchOffsetReusesCachedBound(t *testing.T) {
	calculator := offsetCalculator(20)
	calls := make([]int64, 0)
	probe := func(height int64) (bool, error) {
		calls = append(calls, height)
		return height >= 5, nil
	}

	first, err := calculator.DetectLowerBound(func() (int64, error) { return 8, nil }, probe)
	require.NoError(t, err)
	assert.Equal(t, int64(5), first[0].Bound)

	callsAfterFirst := len(calls)
	second, err := calculator.DetectLowerBound(func() (int64, error) { return 9, nil }, probe)
	require.NoError(t, err)
	assert.Equal(t, int64(5), second[0].Bound)
	// Second run confirms the cached bound with exactly one probe.
	assert.Equal(t, []int64{5}, calls[callsAfterFirst:])
}

// Offset variant: shiftLeftAndSearch tolerates a sporadic missing block below a probed middle.
func TestLowerBoundSearchOffsetToleratesHoleWithinLimit(t *testing.T) {
	calculator := offsetCalculator(20)

	// Data everywhere from 3 up, except a single hole at 6. A plain search probing middle 6
	// would wrongly move higher; the offset scan finds data at 5 just below it.
	result, err := calculator.DetectLowerBound(
		func() (int64, error) { return 12, nil },
		func(height int64) (bool, error) {
			if height == 6 {
				return false, nil
			}
			return height >= 3, nil
		},
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(3), result[0].Bound)
}

// Offset variant: a hole wider than maxOffset is abandoned and the search moves higher.
func TestLowerBoundSearchOffsetAbandonsHoleBeyondLimit(t *testing.T) {
	calculator := offsetCalculator(2)

	// Genuine bound is 9; blocks below it are missing. With maxOffset=2 the downward scan from a
	// no-data middle gives up quickly and the search proceeds upward to the real boundary.
	result, err := calculator.DetectLowerBound(
		func() (int64, error) { return 12, nil },
		func(height int64) (bool, error) { return height >= 9, nil },
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(9), result[0].Bound)
}
