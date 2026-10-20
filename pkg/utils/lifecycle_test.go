package utils_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestLifecycleStartOnlyOneTime(t *testing.T) {
	l := utils.NewGenericLifecycle("name", context.Background())
	tMock := testInterfaceMock{}
	tMock.On("Test").Return(nil)
	f := func(ctx context.Context) error {
		return tMock.Test()
	}

	l.Start(f)
	l.Start(f)

	tMock.AssertNumberOfCalls(t, "Test", 1)
	assert.True(t, l.Running())
}

func TestLifecycleStartAndStop(t *testing.T) {
	l := utils.NewGenericLifecycle("name", context.Background())
	tMock := testInterfaceMock{}
	tMock.On("Test").Return(nil)
	f := func(ctx context.Context) error {
		return tMock.Test()
	}

	l.Start(f)
	l.Stop()
	assert.False(t, l.Running())

	l.Start(f)

	tMock.AssertNumberOfCalls(t, "Test", 2)
	assert.True(t, l.Running())
}

func TestLifecycleCantStart(t *testing.T) {
	l := utils.NewGenericLifecycle("name", context.Background())
	tMock := testInterfaceMock{}
	tMock.On("Test").Return(errors.New("err"))
	f := func(ctx context.Context) error {
		return tMock.Test()
	}

	l.Start(f)

	tMock.AssertNumberOfCalls(t, "Test", 1)
	assert.False(t, l.Running())
}

type testInterfaceMock struct {
	mock.Mock
}

func (t *testInterfaceMock) Test() error {
	return t.Called().Error(0)
}

// A start function that fails leaves nothing behind: the context handed to it
// is cancelled, so whatever it bound to that context (streams, goroutines) is
// released even though Stop() will never run for a lifecycle that never became
// running.
func TestLifecycleFailedStartCancelsItsContext(t *testing.T) {
	lifecycle := utils.NewGenericLifecycle("test", context.Background())
	var startCtx context.Context

	lifecycle.Start(func(ctx context.Context) error {
		startCtx = ctx
		return errors.New("start failed")
	})

	assert.False(t, lifecycle.Running())
	assert.ErrorIs(t, startCtx.Err(), context.Canceled)
}

// Start never runs on a cancelled parent: there is nothing such a run could bind to, and
// marking it running would leave Running() reporting a run that has no goroutines.
func TestLifecycleStartOnCancelledParentDoesNotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lifecycle := utils.NewGenericLifecycle("test", ctx)
	called := false

	lifecycle.Start(func(ctx context.Context) error {
		called = true
		return nil
	})

	assert.False(t, called)
	assert.False(t, lifecycle.Running())
}

// A start that fails after a Stop and a new Start have already run must not clobber the
// newer run: Running() and the set of live run contexts always agree.
func TestLifecycleFailedStartDoesNotClobberNewerRun(t *testing.T) {
	lifecycle := utils.NewGenericLifecycle("test", context.Background())
	entered := make(chan struct{})
	release := make(chan struct{})
	var secondCtx context.Context

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		lifecycle.Start(func(ctx context.Context) error {
			close(entered)
			<-release
			return errors.New("start failed")
		})
	}()
	<-entered

	stopping := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(stopping)
		lifecycle.Stop()
		lifecycle.Start(func(ctx context.Context) error {
			secondCtx = ctx
			return nil
		})
	}()
	// the goroutine is running and about to call Stop; give it a moment to reach the mutex
	// so that releasing the first start is what lets it proceed
	<-stopping
	time.Sleep(50 * time.Millisecond)
	close(release)
	<-done
	<-firstDone

	// whichever order the runtime picked, the second Start is the last transition: it must
	// have run, be reported as running, and hold a live context
	require.NotNil(t, secondCtx, "second Start did not run")
	assert.True(t, lifecycle.Running())
	assert.NoError(t, secondCtx.Err(), "the newer run was clobbered by the failed first start")
}

// StopWith runs its teardown only when the lifecycle actually transitions to stopped, the
// same way Start runs its function only on the transition to running - so a nested
// processor driven from the teardown is never stopped under a running owner, nor stopped
// twice.
func TestLifecycleStopWithRunsTeardownOnlyOnTransition(t *testing.T) {
	lifecycle := utils.NewGenericLifecycle("test", context.Background())
	teardowns := 0
	teardown := func() { teardowns++ }

	lifecycle.StopWith(teardown)
	assert.Equal(t, 0, teardowns)

	lifecycle.Start(func(ctx context.Context) error { return nil })
	lifecycle.StopWith(teardown)
	lifecycle.StopWith(teardown)

	assert.Equal(t, 1, teardowns)
	assert.False(t, lifecycle.Running())
}

// The teardown is part of the stop transition: a concurrent Start waits for it, so an
// owner and the processor it stops in the teardown can never be observed half-switched.
func TestLifecycleStartWaitsForTeardownInProgress(t *testing.T) {
	lifecycle := utils.NewGenericLifecycle("test", context.Background())
	lifecycle.Start(func(ctx context.Context) error { return nil })

	entered := make(chan struct{})
	release := make(chan struct{})
	go lifecycle.StopWith(func() {
		close(entered)
		<-release
	})
	<-entered

	var started atomic.Bool
	starting := make(chan struct{})
	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		close(starting)
		lifecycle.Start(func(ctx context.Context) error {
			started.Store(true)
			return nil
		})
	}()
	// the goroutine is running and about to call Start; a running goroutine reaches the
	// mutex in nanoseconds, so after the pause the only way for Start to be pending is
	// that it waits on the teardown
	<-starting
	time.Sleep(50 * time.Millisecond)
	assert.False(t, started.Load(), "Start ran while a teardown was in progress")

	close(release)
	<-startDone
	assert.True(t, started.Load())
	assert.True(t, lifecycle.Running())
}
