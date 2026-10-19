package utils

import (
	"context"
	"sync"

	"github.com/rs/zerolog/log"
)

type Lifecycle interface {
	Start()
	Stop()
	Running() bool
}

// GenericLifecycle owns one run at a time: Start opens a run under a context derived from
// the parent, Stop cancels it. Start and Stop are idempotent and safe to call from any
// goroutine: every transition happens under one mutex, so Running() only ever reports a
// completed transition, and a Start can never leave a run that no Stop can reach.
//
// Stop does not wait for the run's goroutines; it only cancels their context. A run must
// therefore bind everything it owns to that context and release it on cancellation.
type GenericLifecycle struct {
	name      string
	parentCtx context.Context

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

func NewGenericLifecycle(name string, parentCtx context.Context) *GenericLifecycle {
	return &GenericLifecycle{
		name:      name,
		parentCtx: parentCtx,
	}
}

// Start opens a run by calling f with the run's context. f runs under the lifecycle's
// mutex, so it must return promptly - spawn goroutines, do not block - and must not call
// back into this lifecycle. A failed f leaves the lifecycle stopped with the run's context
// cancelled, so whatever the failed start bound to it is released. Nothing is started on
// a cancelled parent: such a run could never do anything, and reporting it as running
// would be a lie.
func (l *GenericLifecycle) Start(f func(ctx context.Context) error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.running {
		log.Debug().Msgf("lifecycle '%s' is already running", l.name)
		return
	}
	if err := l.parentCtx.Err(); err != nil {
		log.Error().Err(err).Msgf("parent context of '%s' is closed, not starting", l.name)
		return
	}

	ctx, cancel := context.WithCancel(l.parentCtx)
	if err := f(ctx); err != nil {
		log.Error().Err(err).Msgf("failed to start lifecycle '%s'", l.name)
		cancel()
		return
	}
	l.cancel = cancel
	l.running = true
}

func (l *GenericLifecycle) Stop() {
	l.StopWith(nil)
}

// StopWith cancels the run and then executes teardown, both under the lifecycle's mutex and
// only if the lifecycle was running. An owner that stops a nested processor in the teardown
// thereby makes "owner stopped, nested stopped" one transition: a concurrent Start waits
// for the whole of it, so the pair is never observed half-switched. The teardown must not
// call back into this lifecycle.
func (l *GenericLifecycle) StopWith(teardown func()) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.running {
		log.Debug().Msgf("lifecycle '%s' is already stopped", l.name)
		return
	}
	l.running = false
	l.cancel()
	l.cancel = nil
	if teardown != nil {
		teardown()
	}
}

func (l *GenericLifecycle) Running() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

func (l *GenericLifecycle) GetParentContext() context.Context {
	return l.parentCtx
}
