package labels_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func waitForLabel(t *testing.T, ch <-chan lo.Tuple2[string, string], timeout time.Duration) lo.Tuple2[string, string] {
	t.Helper()

	select {
	case label := <-ch:
		return label
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for label after %s", timeout)
		return lo.Tuple2[string, string]{}
	}
}

func assertNoLabel(t *testing.T, ch <-chan lo.Tuple2[string, string], timeout time.Duration) {
	t.Helper()

	select {
	case label := <-ch:
		t.Fatalf("unexpected label published: %+v", label)
	case <-time.After(timeout):
	}
}

func startLabelsProcessor(t *testing.T, processor *labels.BaseLabelsProcessor) {
	t.Helper()

	processor.Start()
	require.Eventually(t, processor.Running, time.Second, 10*time.Millisecond)
}

func stopLabelsProcessor(t *testing.T, processor *labels.BaseLabelsProcessor) {
	t.Helper()

	processor.Stop()
	require.Eventually(t, func() bool { return !processor.Running() }, time.Second, 10*time.Millisecond)
}

func TestNewBaseLabelsProcessorImplementsLabelsProcessor(t *testing.T) {
	detector := mocks.NewLabelsDetectorMock()
	processor := labels.NewBaseLabelsProcessor(context.Background(), "up-1", []labels.LabelsDetector{detector}, time.Second)

	require.NotNil(t, processor)

	var labelProcessor labels.LabelsProcessor = processor
	assert.NotNil(t, labelProcessor)
	assert.False(t, processor.Running())
}

func TestNewBaseLabelsProcessorReturnsNilWhenNoDetectorsProvided(t *testing.T) {
	processor := labels.NewBaseLabelsProcessor(context.Background(), "up-1", nil, time.Second)

	assert.Nil(t, processor)
}

func TestBaseLabelsProcessorSubscribeReturnsSubscription(t *testing.T) {
	detector := mocks.NewLabelsDetectorMock()
	processor := labels.NewBaseLabelsProcessor(context.Background(), "up-1", []labels.LabelsDetector{detector}, time.Second)

	sub := processor.Subscribe("sub-1")
	defer sub.Unsubscribe()

	require.NotNil(t, sub)
	require.NotNil(t, sub.Events)
}

func TestBaseLabelsProcessorPublishesLabelsOnStart(t *testing.T) {
	detector := mocks.NewLabelsDetectorMock()
	detector.On("DetectLabels").Return(map[string]string{
		"client_version": "1.18.23",
		"client_type":    "solana",
	}).Once()

	processor := labels.NewBaseLabelsProcessor(context.Background(), "up-1", []labels.LabelsDetector{detector}, time.Hour)
	sub := processor.Subscribe("sub-1")
	defer sub.Unsubscribe()

	startLabelsProcessor(t, processor)
	defer stopLabelsProcessor(t, processor)

	first := waitForLabel(t, sub.Events, 200*time.Millisecond)
	second := waitForLabel(t, sub.Events, 200*time.Millisecond)

	got := map[string]string{
		first.A:  first.B,
		second.A: second.B,
	}

	assert.Equal(t, "1.18.23", got["client_version"])
	assert.Equal(t, "solana", got["client_type"])
	detector.AssertExpectations(t)
}

func TestBaseLabelsProcessorPublishesLabelsFromMultipleDetectors(t *testing.T) {
	firstDetector := mocks.NewLabelsDetectorMock()
	firstDetector.On("DetectLabels").Return(map[string]string{
		"client_version": "1.18.23",
	}).Once()

	secondDetector := mocks.NewLabelsDetectorMock()
	secondDetector.On("DetectLabels").Return(map[string]string{
		"client_type": "solana",
	}).Once()

	processor := labels.NewBaseLabelsProcessor(
		context.Background(),
		"up-1",
		[]labels.LabelsDetector{firstDetector, secondDetector},
		time.Hour,
	)
	sub := processor.Subscribe("sub-1")
	defer sub.Unsubscribe()

	startLabelsProcessor(t, processor)
	defer stopLabelsProcessor(t, processor)

	first := waitForLabel(t, sub.Events, 200*time.Millisecond)
	second := waitForLabel(t, sub.Events, 200*time.Millisecond)

	got := map[string]string{
		first.A:  first.B,
		second.A: second.B,
	}

	assert.Equal(t, "1.18.23", got["client_version"])
	assert.Equal(t, "solana", got["client_type"])
	firstDetector.AssertExpectations(t)
	secondDetector.AssertExpectations(t)
}

func TestBaseLabelsProcessorPollsDetectorAgainAfterDelay(t *testing.T) {
	detector := mocks.NewLabelsDetectorMock()
	detector.On("DetectLabels").Return(map[string]string{
		"client_version": "1.18.23",
	}).Once()
	detector.On("DetectLabels").Return(map[string]string{
		"client_version": "1.18.24",
	}).Once()
	detector.On("DetectLabels").Return(map[string]string(nil)).Maybe()

	processor := labels.NewBaseLabelsProcessor(context.Background(), "up-1", []labels.LabelsDetector{detector}, 20*time.Millisecond)
	sub := processor.Subscribe("sub-1")
	defer sub.Unsubscribe()

	startLabelsProcessor(t, processor)
	defer stopLabelsProcessor(t, processor)

	first := waitForLabel(t, sub.Events, 200*time.Millisecond)
	second := waitForLabel(t, sub.Events, 200*time.Millisecond)

	assert.Equal(t, lo.T2("client_version", "1.18.23"), first)
	assert.Equal(t, lo.T2("client_version", "1.18.24"), second)
	detector.AssertExpectations(t)
}

func TestBaseLabelsProcessorSkipsEmptyKeysAndValues(t *testing.T) {
	detector := mocks.NewLabelsDetectorMock()
	detector.On("DetectLabels").Return(map[string]string{
		"":               "ignored",
		"client_type":    "",
		"client_version": "1.18.23",
	}).Once()

	processor := labels.NewBaseLabelsProcessor(context.Background(), "up-1", []labels.LabelsDetector{detector}, time.Hour)
	sub := processor.Subscribe("sub-1")
	defer sub.Unsubscribe()

	startLabelsProcessor(t, processor)
	defer stopLabelsProcessor(t, processor)

	label := waitForLabel(t, sub.Events, 200*time.Millisecond)

	assert.Equal(t, lo.T2("client_version", "1.18.23"), label)
	assertNoLabel(t, sub.Events, 50*time.Millisecond)
	detector.AssertExpectations(t)
}
