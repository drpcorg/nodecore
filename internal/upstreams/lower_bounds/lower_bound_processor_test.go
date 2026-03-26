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

func startService(t *testing.T, service *lower_bounds.BaseLowerBoundProcessor) chan struct{} {
	t.Helper()

	done := make(chan struct{})
	go func() {
		service.Start()
		close(done)
	}()

	require.Eventually(t, service.Running, time.Second, 10*time.Millisecond)
	return done
}

func stopService(t *testing.T, service *lower_bounds.BaseLowerBoundProcessor, done chan struct{}) {
	t.Helper()

	service.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for service to stop")
	}
}

func TestNewBaseLowerBoundServiceWithDelayDefaults(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(
		context.Background(),
		"up-1",
		0,
		time.Millisecond,
		[]lower_bounds.LowerBoundDetector{detector},
	)

	assert.False(t, service.Running())
	assert.Equal(t, int64(0), service.PredictLowerBound(protocol.StateBound, 0))
}

func TestNewBaseLowerBoundServiceWithDelayReturnsNilWhenNoDetectorsProvided(t *testing.T) {
	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, nil)

	assert.Nil(t, service)
}

func TestBaseLowerBoundServiceSubscribeReturnsSubscription(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(
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

func TestBaseLowerBoundServiceUsesCustomInitialDelay(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(100, 1000, protocol.StateBound),
	}, nil).Once()
	detector.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, 80*time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
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

func TestBaseLowerBoundServicePublishesBoundsAndPredictsThem(t *testing.T) {
	now := time.Now().Unix()
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(100, now, protocol.StateBound),
		protocol.NewLowerBoundData(200, now, protocol.SlotBound),
	}, nil).Once()
	detector.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
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

func TestBaseLowerBoundServicePredictLowerBoundUsesOffset(t *testing.T) {
	now := time.Now().Unix()
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(100, now, protocol.StateBound),
	}, nil).Once()
	detector.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(context.Background(), "up-1", 1, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
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

func TestBaseLowerBoundServiceIgnoresDetectorErrorAndPublishesOnRetry(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound").Return(nil, errors.New("temporary")).Once()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(101, 1001, protocol.StateBound),
	}, nil).Once()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData(nil), nil).Maybe()
	detector.On("SupportedTypes").Return([]protocol.LowerBoundType{protocol.StateBound}).Maybe()
	detector.On("Period").Return(20 * time.Millisecond).Maybe()

	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()

	done := startService(t, service)
	defer stopService(t, service, done)

	event := waitForLowerBound(t, sub.Events, 300*time.Millisecond)

	assert.Equal(t, protocol.StateBound, event.Type)
	assert.Equal(t, int64(101), event.Bound)
	detector.AssertExpectations(t)
}

func TestBaseLowerBoundServiceIgnoresLowerBoundThatMovesBackwards(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(100, 1000, protocol.StateBound),
	}, nil).Once()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(99, 1001, protocol.StateBound),
	}, nil).Once()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData(nil), nil).Maybe()
	detector.On("Period").Return(20 * time.Millisecond).Maybe()

	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
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

func TestBaseLowerBoundServiceAcceptsArchivalBoundOne(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(100, 1000, protocol.StateBound),
	}, nil).Once()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(1, 1001, protocol.StateBound),
	}, nil).Once()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData(nil), nil).Maybe()
	detector.On("Period").Return(20 * time.Millisecond).Maybe()

	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
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

func TestBaseLowerBoundServicePublishesBoundsFromMultipleDetectors(t *testing.T) {
	d1 := mocks.NewLowerBoundDetectorMock()
	d1.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(50, 1000, protocol.StateBound),
	}, nil).Once()
	d1.On("Period").Return(time.Hour).Maybe()

	d2 := mocks.NewLowerBoundDetectorMock()
	d2.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(70, 1000, protocol.SlotBound),
	}, nil).Once()
	d2.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{d1, d2})
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

func TestBaseLowerBoundServiceStopStopsLifecycle(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(10, 1000, protocol.StateBound),
	}, nil).Maybe()
	detector.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})

	done := startService(t, service)
	stopService(t, service, done)

	assert.False(t, service.Running())
	detector.AssertExpectations(t)
}

func TestBaseLowerBoundServiceSecondStartDoesNotDuplicatePublishing(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound").Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(77, 1000, protocol.StateBound),
	}, nil).Once()
	detector.On("Period").Return(time.Hour).Maybe()

	service := lower_bounds.NewBaseLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
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
