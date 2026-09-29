package lower_bounds_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func waitForLowerBound(t *testing.T, ch <-chan protocol.LowerBoundData, timeout time.Duration) protocol.LowerBoundData {
	t.Helper()

	select {
	case bound := <-ch:
		return bound
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for lower bound after %s", timeout)
		return protocol.LowerBoundData{}
	}
}

func assertNoLowerBound(t *testing.T, ch <-chan protocol.LowerBoundData, timeout time.Duration) {
	t.Helper()

	select {
	case bound := <-ch:
		t.Fatalf("unexpected lower bound published: %+v", bound)
	case <-time.After(timeout):
	}
}

func startService(t *testing.T, service *lower_bounds.GenericLowerBoundProcessor) chan struct{} {
	t.Helper()

	done := make(chan struct{})
	go func() {
		service.Start()
		close(done)
	}()

	require.Eventually(t, service.Running, time.Second, 10*time.Millisecond)
	return done
}

func stopService(t *testing.T, service *lower_bounds.GenericLowerBoundProcessor, done chan struct{}) {
	t.Helper()

	service.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for service to stop")
	}
}

func TestNewGenericLowerBoundServiceWithDelayDefaults(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(
		context.Background(),
		"up-1",
		0,
		time.Millisecond,
		[]lower_bounds.LowerBoundDetector{detector},
	)

	assert.False(t, service.Running())
	assert.Equal(t, int64(0), service.PredictLowerBound(protocol.StateBound, 0))
}

func TestNewGenericLowerBoundServiceWithDelayReturnsNilWhenNoDetectorsProvided(t *testing.T) {
	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, nil)

	assert.Nil(t, service)
}

func TestGenericLowerBoundServiceSubscribeReturnsSubscription(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(
		context.Background(),
		"up-1",
		0,
		time.Millisecond,
		[]lower_bounds.LowerBoundDetector{detector},
	)

	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()

	require.NotNil(t, sub)
	require.NotNil(t, sub.Events)
}

func TestGenericLowerBoundServiceUsesCustomInitialDelay(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(100, 1000, protocol.StateBound),
	}, nil).Once()
	detector.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, 80*time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()

	done := startService(t, service)
	defer stopService(t, service, done)

	assertNoLowerBound(t, sub.Events, 30*time.Millisecond)
	event := waitForLowerBound(t, sub.Events, 200*time.Millisecond)

	assert.Equal(t, protocol.StateBound, event.Type)
	assert.Equal(t, int64(100), event.Bound)
	detector.AssertExpectations(t)
}

func TestGenericLowerBoundServicePublishesBoundsAndPredictsThem(t *testing.T) {
	now := time.Now().Unix()
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(100, now, protocol.StateBound),
		protocol.NewLowerBoundData(200, now, protocol.SlotBound),
	}, nil).Once()
	detector.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()

	done := startService(t, service)
	defer stopService(t, service, done)

	first := waitForLowerBound(t, sub.Events, 50*time.Millisecond)
	second := waitForLowerBound(t, sub.Events, 50*time.Millisecond)

	assert.Equal(t, protocol.StateBound, first.Type)
	assert.Equal(t, int64(100), first.Bound)
	assert.Equal(t, protocol.SlotBound, second.Type)
	assert.Equal(t, int64(200), second.Bound)
	assert.Equal(t, int64(100), service.PredictLowerBound(protocol.StateBound, 0))
	assert.Equal(t, int64(200), service.PredictLowerBound(protocol.SlotBound, 0))
	detector.AssertExpectations(t)
}

func TestGenericLowerBoundServicePredictLowerBoundUsesOffset(t *testing.T) {
	now := time.Now().Unix()
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(100, now, protocol.StateBound),
	}, nil).Once()
	detector.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 1, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()

	done := startService(t, service)
	defer stopService(t, service, done)

	_ = waitForLowerBound(t, sub.Events, 200*time.Millisecond)

	predicted := service.PredictLowerBound(protocol.StateBound, 10)
	assert.GreaterOrEqual(t, predicted, int64(109))
	assert.LessOrEqual(t, predicted, int64(111))
	detector.AssertExpectations(t)
}

func TestGenericLowerBoundServiceIgnoresDetectorErrorAndPublishesOnRetry(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound", mock.Anything).Return(nil, errors.New("temporary")).Once()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(101, 1001, protocol.StateBound),
	}, nil).Once()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData(nil), nil).Maybe()
	detector.On("SupportedTypes").Return([]protocol.LowerBoundType{protocol.StateBound}).Maybe()
	detector.On("Period").Return(20 * time.Millisecond).Maybe()

	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()

	done := startService(t, service)
	defer stopService(t, service, done)

	event := waitForLowerBound(t, sub.Events, 300*time.Millisecond)

	assert.Equal(t, protocol.StateBound, event.Type)
	assert.Equal(t, int64(101), event.Bound)
	detector.AssertExpectations(t)
}

func TestGenericLowerBoundServiceIgnoresLowerBoundThatMovesBackwards(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(100, 1000, protocol.StateBound),
	}, nil).Once()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(99, 1001, protocol.StateBound),
	}, nil).Once()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData(nil), nil).Maybe()
	detector.On("Period").Return(20 * time.Millisecond).Maybe()

	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()

	done := startService(t, service)
	defer stopService(t, service, done)

	first := waitForLowerBound(t, sub.Events, 200*time.Millisecond)

	assert.Equal(t, int64(100), first.Bound)
	assertNoLowerBound(t, sub.Events, 100*time.Millisecond)
	assert.Equal(t, int64(100), service.PredictLowerBound(protocol.StateBound, 0))
	detector.AssertExpectations(t)
}

func TestGenericLowerBoundServiceAcceptsArchivalBoundOne(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(100, 1000, protocol.StateBound),
	}, nil).Once()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(1, 1001, protocol.StateBound),
	}, nil).Once()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData(nil), nil).Maybe()
	detector.On("Period").Return(20 * time.Millisecond).Maybe()

	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()

	done := startService(t, service)
	defer stopService(t, service, done)

	first := waitForLowerBound(t, sub.Events, 50*time.Millisecond)
	second := waitForLowerBound(t, sub.Events, 50*time.Millisecond)

	assert.Equal(t, int64(100), first.Bound)
	assert.Equal(t, int64(1), second.Bound)
	assert.Equal(t, int64(1), service.PredictLowerBound(protocol.StateBound, 0))
	detector.AssertExpectations(t)
}

func TestGenericLowerBoundServicePublishesBoundsFromMultipleDetectors(t *testing.T) {
	d1 := mocks.NewLowerBoundDetectorMock()
	d1.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(50, 1000, protocol.StateBound),
	}, nil).Once()
	d1.On("Period").Return(time.Hour).Maybe()

	d2 := mocks.NewLowerBoundDetectorMock()
	d2.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(70, 1000, protocol.SlotBound),
	}, nil).Once()
	d2.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{d1, d2})
	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()

	done := startService(t, service)
	defer stopService(t, service, done)

	first := waitForLowerBound(t, sub.Events, 50*time.Millisecond)
	second := waitForLowerBound(t, sub.Events, 50*time.Millisecond)

	got := map[protocol.LowerBoundType]int64{
		first.Type:  first.Bound,
		second.Type: second.Bound,
	}

	assert.Equal(t, int64(50), got[protocol.StateBound])
	assert.Equal(t, int64(70), got[protocol.SlotBound])
	d1.AssertExpectations(t)
	d2.AssertExpectations(t)
}

func TestGenericLowerBoundServiceStopStopsLifecycle(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(10, 1000, protocol.StateBound),
	}, nil).Maybe()
	detector.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})

	done := startService(t, service)
	stopService(t, service, done)

	assert.False(t, service.Running())
	detector.AssertExpectations(t)
}

func TestGenericLowerBoundServiceSecondStartDoesNotDuplicatePublishing(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(77, 1000, protocol.StateBound),
	}, nil).Once()
	detector.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()

	done := startService(t, service)
	defer stopService(t, service, done)

	service.Start()

	event := waitForLowerBound(t, sub.Events, 200*time.Millisecond)
	assert.Equal(t, int64(77), event.Bound)
	assertNoLowerBound(t, sub.Events, 100*time.Millisecond)
	detector.AssertExpectations(t)
}

func TestWithManualBoundsSkipsFullyConfiguredDetectorsAndOverridesDetectedTypes(t *testing.T) {
	stateDetector := mocks.NewLowerBoundDetectorMock()
	stateDetector.On("SupportedTypes").Return([]protocol.LowerBoundType{protocol.StateBound, protocol.TraceBound})
	stateDetector.On("Period").Return(time.Hour).Maybe()

	blockDetector := mocks.NewLowerBoundDetectorMock()
	blockDetector.On("SupportedTypes").Return([]protocol.LowerBoundType{protocol.BlockBound, protocol.LogsBound})
	blockDetector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(50, 1000, protocol.BlockBound),
		protocol.NewLowerBoundData(60, 1000, protocol.LogsBound),
	}, nil).Once()
	blockDetector.On("Period").Return(time.Hour).Maybe()

	ctx := context.Background()
	detected := lower_bounds.NewGenericLowerBoundProcessorWithDelay(ctx, "up-1", 1, time.Millisecond, []lower_bounds.LowerBoundDetector{stateDetector, blockDetector})
	manual := map[protocol.LowerBoundType]int64{protocol.StateBound: 1, protocol.TraceBound: 1, protocol.BlockBound: 10}
	processor := lower_bounds.WithManualBounds(ctx, "up-1", manual, detected)
	sub := processor.Subscribe("sub-1")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()

	published := make(map[protocol.LowerBoundType]int64)
	for range manual {
		event := waitForLowerBound(t, sub.Events, time.Second)
		published[event.Type] = event.Bound
	}
	assert.Equal(t, manual, published)

	event := waitForLowerBound(t, sub.Events, time.Second)
	assert.Equal(t, protocol.LogsBound, event.Type)
	assert.Equal(t, int64(60), event.Bound)
	assertNoLowerBound(t, sub.Events, 100*time.Millisecond)

	// configured bounds are static: a non-zero average speed must not move them
	assert.Equal(t, int64(10), processor.PredictLowerBound(protocol.BlockBound, 1000))
	assert.Equal(t, int64(1), processor.PredictLowerBound(protocol.StateBound, 1000))
	stateDetector.AssertNotCalled(t, "DetectLowerBound", mock.Anything)
	blockDetector.AssertExpectations(t)
}

func TestWithManualBoundsWithoutDetectedProcessor(t *testing.T) {
	tests := []struct {
		name     string
		detected lower_bounds.LowerBoundProcessor
	}{
		{name: "nil", detected: nil},
		{name: "typed nil", detected: (*lower_bounds.GenericLowerBoundProcessor)(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := lower_bounds.WithManualBounds(context.Background(), "up-1", map[protocol.LowerBoundType]int64{protocol.StateBound: 5}, tt.detected)
			require.NotNil(t, processor)
			sub := processor.Subscribe("sub-1")
			defer sub.Unsubscribe()
			processor.Start()
			defer processor.Stop()

			event := waitForLowerBound(t, sub.Events, time.Second)
			assert.Equal(t, protocol.StateBound, event.Type)
			assert.Equal(t, int64(5), event.Bound)
			assertNoLowerBound(t, sub.Events, 50*time.Millisecond)
			assert.True(t, processor.Running())
			assert.Equal(t, int64(5), processor.PredictLowerBound(protocol.StateBound, 0))
		})
	}
}
