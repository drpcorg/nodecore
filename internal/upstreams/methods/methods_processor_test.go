package methods_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedDetector returns a different verdict per round so a test can tell the
// immediate round from the ticker rounds.
type scriptedDetector struct {
	rounds []mapset.Set[string]
	calls  atomic.Int64
}

func (s *scriptedDetector) DetectUnsupported(_ context.Context) mapset.Set[string] {
	index := int(s.calls.Add(1)) - 1
	if index >= len(s.rounds) {
		index = len(s.rounds) - 1
	}
	return s.rounds[index]
}

func TestNewGenericMethodsProcessorNilWithoutDetectors(t *testing.T) {
	processor := methods.NewGenericMethodsProcessor(context.Background(), "upstream-1", nil, time.Minute)

	assert.Nil(t, processor)
}

func TestGenericMethodsProcessorPublishesFirstRoundImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	detector := &scriptedDetector{rounds: []mapset.Set[string]{
		mapset.NewThreadUnsafeSet[string]("trace_block"),
	}}
	processor := methods.NewGenericMethodsProcessor(ctx, "upstream-1", []methods.MethodsDetector{detector}, time.Hour)
	require.NotNil(t, processor)

	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()

	published := receiveVerdict(t, sub.Events)
	assert.True(t, mapset.NewThreadUnsafeSet[string]("trace_block").Equal(published))
}

func TestGenericMethodsProcessorUnionsDetectors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := &scriptedDetector{rounds: []mapset.Set[string]{mapset.NewThreadUnsafeSet[string]("trace_block")}}
	second := &scriptedDetector{rounds: []mapset.Set[string]{mapset.NewThreadUnsafeSet[string]("debug_storageRangeAt")}}

	processor := methods.NewGenericMethodsProcessor(
		ctx, "upstream-1", []methods.MethodsDetector{first, second}, time.Hour,
	)
	require.NotNil(t, processor)

	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()

	published := receiveVerdict(t, sub.Events)
	expected := mapset.NewThreadUnsafeSet[string]("trace_block", "debug_storageRangeAt")
	assert.True(t, expected.Equal(published), "expected %v, got %v", expected.ToSlice(), published.ToSlice())
}

func TestGenericMethodsProcessorToleratesDetectorWithNoOpinion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	silent := &scriptedDetector{rounds: []mapset.Set[string]{nil}}
	speaking := &scriptedDetector{rounds: []mapset.Set[string]{mapset.NewThreadUnsafeSet[string]("trace_block")}}

	processor := methods.NewGenericMethodsProcessor(
		ctx, "upstream-1", []methods.MethodsDetector{silent, speaking}, time.Hour,
	)
	require.NotNil(t, processor)

	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()

	published := receiveVerdict(t, sub.Events)
	assert.True(t, mapset.NewThreadUnsafeSet[string]("trace_block").Equal(published), "a nil verdict must not break the union")
}

func TestGenericMethodsProcessorRepublishesOnlyOnChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Rounds 1 and 2 agree, round 3 differs. Only rounds 1 and 3 should publish.
	detector := &scriptedDetector{rounds: []mapset.Set[string]{
		mapset.NewThreadUnsafeSet[string]("trace_block"),
		mapset.NewThreadUnsafeSet[string]("trace_block"),
		mapset.NewThreadUnsafeSet[string]("trace_block", "debug_storageRangeAt"),
	}}

	processor := methods.NewGenericMethodsProcessor(
		ctx, "upstream-1", []methods.MethodsDetector{detector}, 50*time.Millisecond,
	)
	require.NotNil(t, processor)

	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()

	firstPublished := receiveVerdict(t, sub.Events)
	assert.True(t, mapset.NewThreadUnsafeSet[string]("trace_block").Equal(firstPublished))

	// The identical second round must be swallowed, so the next value to arrive is
	// round three's.
	secondPublished := receiveVerdict(t, sub.Events)
	expected := mapset.NewThreadUnsafeSet[string]("trace_block", "debug_storageRangeAt")
	assert.True(t, expected.Equal(secondPublished), "expected %v, got %v", expected.ToSlice(), secondPublished.ToSlice())
	assert.GreaterOrEqual(t, detector.calls.Load(), int64(3), "the ticker must keep running rounds")
}

func receiveVerdict(t *testing.T, events <-chan mapset.Set[string]) mapset.Set[string] {
	t.Helper()

	select {
	case published := <-events:
		return published
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a verdict")
		return nil
	}
}
