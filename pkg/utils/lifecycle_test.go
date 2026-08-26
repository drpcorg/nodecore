package utils_test

import (
	"context"
	"errors"
	"testing"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
