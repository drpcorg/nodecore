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
	f := func(ctx context.Context) (func(), error) {
		return nil, tMock.Test()
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
	f := func(ctx context.Context) (func(), error) {
		return nil, tMock.Test()
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
	f := func(ctx context.Context) (func(), error) {
		return nil, tMock.Test()
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

	lifecycle.Start(func(ctx context.Context) (func(), error) {
		startCtx = ctx
		return nil, errors.New("start failed")
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

	lifecycle.Start(func(ctx context.Context) (func(), error) {
		called = true
		return nil, nil
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
		lifecycle.Start(func(ctx context.Context) (func(), error) {
			close(entered)
			<-release
			return nil, errors.New("start failed")
		})
	}()
	<-entered

	stopping := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(stopping)
		lifecycle.Stop()
		lifecycle.Start(func(ctx context.Context) (func(), error) {
			secondCtx = ctx
			return nil, nil
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

// The teardown a start returns is run by Stop exactly once, on the running→stopped
// transition, after the run's context is cancelled - so a nested processor released in the
// teardown is never stopped under a running owner, nor stopped twice.
func TestLifecycleStopRunsStartsTeardownOnce(t *testing.T) {
	lifecycle := utils.NewGenericLifecycle("test", context.Background())
	teardowns := 0
	var runCtx context.Context

	lifecycle.Start(func(ctx context.Context) (func(), error) {
		runCtx = ctx
		return func() {
			assert.ErrorIs(t, ctx.Err(), context.Canceled, "teardown ran before the run was cancelled")
			teardowns++
		}, nil
	})
	lifecycle.Stop()
	lifecycle.Stop()

	assert.Equal(t, 1, teardowns)
	assert.ErrorIs(t, runCtx.Err(), context.Canceled)
	assert.False(t, lifecycle.Running())
}

// A start that fails after building something returns the undo for it, and the lifecycle
// runs that undo at once: a failed start leaves nothing behind for a Stop that will never
// come, because the lifecycle never became running.
func TestLifecycleFailedStartRunsItsTeardown(t *testing.T) {
	lifecycle := utils.NewGenericLifecycle("test", context.Background())
	teardowns := 0

	lifecycle.Start(func(ctx context.Context) (func(), error) {
		return func() {
			assert.ErrorIs(t, ctx.Err(), context.Canceled, "teardown ran before the run was cancelled")
			teardowns++
		}, errors.New("start failed")
	})

	assert.Equal(t, 1, teardowns)
	assert.False(t, lifecycle.Running())
	lifecycle.Stop()
	assert.Equal(t, 1, teardowns, "Stop re-ran the teardown of a start that never became running")
}

// The teardown is part of the stop transition: a concurrent Start waits for it, so an
// owner and the processor it releases in the teardown can never be observed half-switched.
func TestLifecycleStartWaitsForTeardownInProgress(t *testing.T) {
	lifecycle := utils.NewGenericLifecycle("test", context.Background())
	entered := make(chan struct{})
	release := make(chan struct{})
	lifecycle.Start(func(ctx context.Context) (func(), error) {
		return func() {
			close(entered)
			<-release
		}, nil
	})

	go lifecycle.Stop()
	<-entered

	var started atomic.Bool
	starting := make(chan struct{})
	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		close(starting)
		lifecycle.Start(func(ctx context.Context) (func(), error) {
			started.Store(true)
			return nil, nil
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
