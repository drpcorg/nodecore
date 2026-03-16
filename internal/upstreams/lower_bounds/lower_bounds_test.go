package lower_bounds_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func mustParseUTC(t *testing.T, s string) time.Time {
	t.Helper()

	tt, err := time.ParseInLocation("02.01.2006 15:04:05", s, time.UTC)
	if err != nil {
		t.Fatalf("failed to parse time %q: %v", s, err)
	}
	return tt
}

func assertLastBound(t *testing.T, lb *lower_bounds.LowerBounds, typ protocol.LowerBoundType, expected protocol.LowerBoundData) {
	t.Helper()

	got, ok := lb.GetLastBound(typ)
	assert.True(t, ok)
	assert.Equal(t, expected, got)
}

func assertNoLastBound(t *testing.T, lb *lower_bounds.LowerBounds, typ protocol.LowerBoundType) {
	t.Helper()

	_, ok := lb.GetLastBound(typ)
	assert.False(t, ok)
}

func TestPredictionAtSpecificTime(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("0g-mainnet").AverageRemoveSpeed())

	dateStr := "28.08.2025 11:00:57"
	reqStr := "28.08.2025 11:10:25"

	baseTime := mustParseUTC(t, dateStr)
	reqTime := mustParseUTC(t, reqStr)

	lb.UpdateBound(protocol.LowerBoundData{
		Bound:     3294252,
		Timestamp: baseTime.Unix(),
		Type:      protocol.ReceiptsBound,
	})
	lb.UpdateBound(protocol.LowerBoundData{
		Bound:     3294552,
		Timestamp: baseTime.Add(3 * time.Minute).Unix(),
		Type:      protocol.ReceiptsBound,
	})
	lb.UpdateBound(protocol.LowerBoundData{
		Bound:     3294552,
		Timestamp: baseTime.Add(6 * time.Minute).Unix(),
		Type:      protocol.ReceiptsBound,
	})

	predicted := lb.PredictNextBoundAtSpecificTime(protocol.ReceiptsBound, reqTime.Unix())
	assert.Equal(t, int64(3294725), predicted)
}

func TestFirstArchivalLowerBoundDataGetItAndPredictTheNextBound(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("ethereum").AverageRemoveSpeed())
	newLowerBound := protocol.LowerBoundData{
		Bound:     1,
		Timestamp: 1000,
		Type:      protocol.StateBound,
	}

	lb.UpdateBound(newLowerBound)
	assertLastBound(t, lb, protocol.StateBound, newLowerBound)

	predictedNextBound := lb.PredictNextBound(protocol.StateBound, 0)
	assert.Equal(t, int64(1), predictedNextBound)

	allBounds := lb.GetAllBounds(protocol.StateBound)
	assert.Equal(t, []protocol.LowerBoundData{newLowerBound}, allBounds)
}

func TestIfNoBoundTheDefaultValues(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("ethereum").AverageRemoveSpeed())

	assertNoLastBound(t, lb, protocol.StateBound)

	predictedNextBound := lb.PredictNextBound(protocol.StateBound, 0)
	assert.Equal(t, predictedNextBound, int64(0))

	allBounds := lb.GetAllBounds(protocol.StateBound)
	assert.Equal(t, []protocol.LowerBoundData(nil), allBounds)
}

func TestSequentialArchivalLowerBoundDataAndGetOnlyTheLast(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("ethereum").AverageRemoveSpeed())

	b1 := protocol.LowerBoundData{
		Bound:     1,
		Timestamp: 1000,
		Type:      protocol.StateBound,
	}
	b2 := protocol.LowerBoundData{
		Bound:     1,
		Timestamp: 1005,
		Type:      protocol.StateBound,
	}

	lb.UpdateBound(b1)
	assertLastBound(t, lb, protocol.StateBound, b1)

	predicted := lb.PredictNextBound(protocol.StateBound, 0)
	assert.Equal(t, int64(1), predicted)
	assert.Equal(t, []protocol.LowerBoundData{b1}, lb.GetAllBounds(protocol.StateBound))

	lb.UpdateBound(b2)
	assertLastBound(t, lb, protocol.StateBound, b2)

	predicted = lb.PredictNextBound(protocol.StateBound, 0)
	assert.Equal(t, int64(1), predicted)
	assert.Equal(t, []protocol.LowerBoundData{b2}, lb.GetAllBounds(protocol.StateBound))
}

func TestDontUpdateTheLowerBoundsIfTheSameTimestamp(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("ethereum").AverageRemoveSpeed())

	b1 := protocol.LowerBoundData{
		Bound:     1,
		Timestamp: 1000,
		Type:      protocol.StateBound,
	}

	lb.UpdateBound(b1)
	lb.UpdateBound(protocol.LowerBoundData{
		Bound:     100000,
		Timestamp: 1000,
		Type:      protocol.StateBound,
	})

	assertLastBound(t, lb, protocol.StateBound, b1)

	predicted := lb.PredictNextBound(protocol.StateBound, 0)
	assert.Equal(t, int64(1), predicted)
	assert.Equal(t, []protocol.LowerBoundData{b1}, lb.GetAllBounds(protocol.StateBound))
}

func TestAlwaysGetTheLastBound(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("ethereum").AverageRemoveSpeed())

	b1 := protocol.LowerBoundData{Bound: 1000, Timestamp: 1000, Type: protocol.StateBound}
	b2 := protocol.LowerBoundData{Bound: 1005, Timestamp: 1005, Type: protocol.StateBound}
	b3 := protocol.LowerBoundData{Bound: 1010, Timestamp: 1010, Type: protocol.StateBound}

	lb.UpdateBound(b1)
	assertLastBound(t, lb, protocol.StateBound, b1)
	assert.Equal(t, []protocol.LowerBoundData{b1}, lb.GetAllBounds(protocol.StateBound))

	lb.UpdateBound(b2)
	assertLastBound(t, lb, protocol.StateBound, b2)
	assert.Equal(t, []protocol.LowerBoundData{b1, b2}, lb.GetAllBounds(protocol.StateBound))

	lb.UpdateBound(b3)
	assertLastBound(t, lb, protocol.StateBound, b3)
	assert.Equal(t, []protocol.LowerBoundData{b1, b2, b3}, lb.GetAllBounds(protocol.StateBound))
}

func TestPreserveTheMaximumNumberOfBounds(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("ethereum").AverageRemoveSpeed())

	b1 := protocol.LowerBoundData{Bound: 1000, Timestamp: 1000, Type: protocol.StateBound}
	b2 := protocol.LowerBoundData{Bound: 1005, Timestamp: 1005, Type: protocol.StateBound}
	b3 := protocol.LowerBoundData{Bound: 1010, Timestamp: 1010, Type: protocol.StateBound}
	b4 := protocol.LowerBoundData{Bound: 1050, Timestamp: 1050, Type: protocol.StateBound}
	b5 := protocol.LowerBoundData{Bound: 1060, Timestamp: 1060, Type: protocol.StateBound}

	lb.UpdateBound(b1)
	lb.UpdateBound(b2)
	lb.UpdateBound(b3)

	assertLastBound(t, lb, protocol.StateBound, b3)
	assert.Equal(t, []protocol.LowerBoundData{b1, b2, b3}, lb.GetAllBounds(protocol.StateBound))

	lb.UpdateBound(b4)
	assertLastBound(t, lb, protocol.StateBound, b4)
	assert.Equal(t, []protocol.LowerBoundData{b2, b3, b4}, lb.GetAllBounds(protocol.StateBound))

	lb.UpdateBound(b5)
	assertLastBound(t, lb, protocol.StateBound, b5)
	assert.Equal(t, []protocol.LowerBoundData{b3, b4, b5}, lb.GetAllBounds(protocol.StateBound))
}

func TestIfGetTheArchivalBoundThenRemovePreviousOnes(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("ethereum").AverageRemoveSpeed())

	b1 := protocol.LowerBoundData{Bound: 1000, Timestamp: 1000, Type: protocol.StateBound}
	b2 := protocol.LowerBoundData{Bound: 1005, Timestamp: 1005, Type: protocol.StateBound}
	b3 := protocol.LowerBoundData{Bound: 1, Timestamp: 1010, Type: protocol.StateBound}

	lb.UpdateBound(b1)
	lb.UpdateBound(b2)

	assertLastBound(t, lb, protocol.StateBound, b2)
	assert.Equal(t, []protocol.LowerBoundData{b1, b2}, lb.GetAllBounds(protocol.StateBound))

	lb.UpdateBound(b3)

	assertLastBound(t, lb, protocol.StateBound, b3)
	assert.Equal(t, []protocol.LowerBoundData{b3}, lb.GetAllBounds(protocol.StateBound))
}

func TestPredictTheSameBoundIfAllBoundsAreEqualToEachOther(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("ethereum").AverageRemoveSpeed())

	b1 := protocol.LowerBoundData{Bound: 15060, Timestamp: 1000, Type: protocol.StateBound}
	b2 := protocol.LowerBoundData{Bound: 15060, Timestamp: 2000, Type: protocol.StateBound}
	b3 := protocol.LowerBoundData{Bound: 15060, Timestamp: 3000, Type: protocol.StateBound}

	lb.UpdateBound(b1)
	lb.UpdateBound(b2)
	lb.UpdateBound(b3)

	assertLastBound(t, lb, protocol.StateBound, b3)

	predicted := lb.PredictNextBound(protocol.StateBound, 0)
	assert.Equal(t, int64(15060), predicted)
	assert.Equal(t, []protocol.LowerBoundData{b1, b2, b3}, lb.GetAllBounds(protocol.StateBound))
}

func TestPredictTheNextBoundBasedOnDifferentBounds(t *testing.T) {
	now := time.Now()
	lb := lower_bounds.NewLowerBounds(chains.GetChain("bsc").AverageRemoveSpeed())

	b1 := protocol.LowerBoundData{
		Bound:     37995846,
		Timestamp: now.Add(-9 * time.Minute).Unix(),
		Type:      protocol.StateBound,
	}
	b2 := protocol.LowerBoundData{
		Bound:     37995906,
		Timestamp: now.Add(-6 * time.Minute).Unix(),
		Type:      protocol.StateBound,
	}
	b3 := protocol.LowerBoundData{
		Bound:     37995966,
		Timestamp: now.Add(-3 * time.Minute).Unix(),
		Type:      protocol.StateBound,
	}

	lb.UpdateBound(b1)
	lb.UpdateBound(b2)
	lb.UpdateBound(b3)

	predicted := lb.PredictNextBound(protocol.StateBound, 0)

	assert.Less(t, predicted, int64(37996030))
	assert.Greater(t, predicted, int64(37996020))
}

func TestPredictTheNextBoundBasedOnAverageSpeed(t *testing.T) {
	now := time.Now()
	lb := lower_bounds.NewLowerBounds(chains.GetChain("bsc").AverageRemoveSpeed())

	b1 := protocol.LowerBoundData{
		Bound:     37995966,
		Timestamp: now.Add(-3 * time.Minute).Unix(),
		Type:      protocol.StateBound,
	}

	lb.UpdateBound(b1)

	predicted := lb.PredictNextBound(protocol.StateBound, 0)
	assert.Less(t, predicted, int64(37996210))
	assert.Greater(t, predicted, int64(37996200))
}

func TestResetAllBoundIfTheNextBoundIsLessThanTheCurrentOne(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("ethereum").AverageRemoveSpeed())

	b1 := protocol.LowerBoundData{Bound: 15060, Timestamp: 1010, Type: protocol.StateBound}
	b2 := protocol.LowerBoundData{Bound: 100, Timestamp: 1020, Type: protocol.StateBound}
	b3 := protocol.LowerBoundData{Bound: 105, Timestamp: 1030, Type: protocol.StateBound}
	b4 := protocol.LowerBoundData{Bound: 108, Timestamp: 1040, Type: protocol.StateBound}
	b5 := protocol.LowerBoundData{Bound: 5, Timestamp: 1050, Type: protocol.StateBound}

	lb.UpdateBound(b1)
	lb.UpdateBound(b2)

	all := lb.GetAllBounds(protocol.StateBound)
	if len(all) != 1 {
		t.Fatalf("unexpected bounds size after reset: got %d, want %d", len(all), 1)
	}
	assertLastBound(t, lb, protocol.StateBound, b2)

	lb.UpdateBound(b3)
	lb.UpdateBound(b4)

	all = lb.GetAllBounds(protocol.StateBound)
	assert.Len(t, all, 3)
	assertLastBound(t, lb, protocol.StateBound, b4)

	lb.UpdateBound(b5)

	all = lb.GetAllBounds(protocol.StateBound)
	assert.Len(t, all, 1)
	assertLastBound(t, lb, protocol.StateBound, b5)
}

func TestResetAllBoundIfTheNextBoundIsMuchBiggerThanTheCurrentOne(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("ethereum").AverageRemoveSpeed())

	b1 := protocol.LowerBoundData{Bound: 15060, Timestamp: 1010, Type: protocol.StateBound}
	b2 := protocol.LowerBoundData{Bound: 130000, Timestamp: 1020, Type: protocol.StateBound}
	b3 := protocol.LowerBoundData{Bound: 131000, Timestamp: 1030, Type: protocol.StateBound}
	b4 := protocol.LowerBoundData{Bound: 132000, Timestamp: 1040, Type: protocol.StateBound}
	b5 := protocol.LowerBoundData{Bound: 232000, Timestamp: 1050, Type: protocol.StateBound}

	lb.UpdateBound(b1)
	lb.UpdateBound(b2)

	all := lb.GetAllBounds(protocol.StateBound)
	assert.Len(t, all, 1)
	assertLastBound(t, lb, protocol.StateBound, b2)

	lb.UpdateBound(b3)
	lb.UpdateBound(b4)

	all = lb.GetAllBounds(protocol.StateBound)
	assert.Len(t, all, 3)
	assertLastBound(t, lb, protocol.StateBound, b4)

	lb.UpdateBound(b5)

	all = lb.GetAllBounds(protocol.StateBound)
	assert.Len(t, all, 1)
	assertLastBound(t, lb, protocol.StateBound, b5)
}

func TestUpdateDifferentBounds(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(chains.GetChain("ethereum").AverageRemoveSpeed())

	state1 := protocol.LowerBoundData{Bound: 15060, Timestamp: 1010, Type: protocol.StateBound}
	state2 := protocol.LowerBoundData{Bound: 16060, Timestamp: 1020, Type: protocol.StateBound}
	state3 := protocol.LowerBoundData{Bound: 17060, Timestamp: 1030, Type: protocol.StateBound}

	block1 := protocol.LowerBoundData{Bound: 20000, Timestamp: 1010, Type: protocol.BlockBound}
	block2 := protocol.LowerBoundData{Bound: 21000, Timestamp: 1020, Type: protocol.BlockBound}
	block3 := protocol.LowerBoundData{Bound: 22000, Timestamp: 1030, Type: protocol.BlockBound}

	lb.UpdateBound(state1)
	lb.UpdateBound(state2)
	lb.UpdateBound(state3)
	lb.UpdateBound(block1)
	lb.UpdateBound(block2)
	lb.UpdateBound(block3)

	assertLastBound(t, lb, protocol.StateBound, state3)
	assert.Equal(t, []protocol.LowerBoundData{state1, state2, state3}, lb.GetAllBounds(protocol.StateBound))

	assertLastBound(t, lb, protocol.BlockBound, block3)
	assert.Equal(t, []protocol.LowerBoundData{block1, block2, block3}, lb.GetAllBounds(protocol.BlockBound))
}
