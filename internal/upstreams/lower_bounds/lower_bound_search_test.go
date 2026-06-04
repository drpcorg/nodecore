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

func TestLowerBoundSearchCalculatorBinarySearchIntegration(t *testing.T) {
	calculator := lower_bounds.NewLowerBoundSearchCalculator("up-1", protocol.BlockBound, time.Minute)
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
	assert.Equal(t, []int64{1, 5, 3, 4}, calls)
}

func TestLowerBoundSearchCalculatorRetriesUnexpectedProbeErrors(t *testing.T) {
	calculator := lower_bounds.NewLowerBoundSearchCalculator("up-1", protocol.BlockBound, time.Minute)
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
	assert.Equal(t, 2, attempts)
}

func TestLowerBoundSearchCalculatorDoesNotRetryUnavailableResult(t *testing.T) {
	calculator := lower_bounds.NewLowerBoundSearchCalculator("up-1", protocol.BlockBound, time.Minute)
	calls := 0

	result, err := calculator.DetectLowerBound(
		func() (int64, error) { return 1, nil },
		func(height int64) (bool, error) {
			calls++
			return false, nil
		},
	)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, 1, calls)
}
