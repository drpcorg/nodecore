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

// StartFunc opens a run on ctx and returns how to undo it. The teardown covers what the run
// built outside ctx - a nested processor, connectors, a drain to wait for - and may be nil
// when cancelling ctx releases everything. On error the lifecycle runs the teardown at once,
// so a failed start leaves nothing behind for a Stop that will never come; on success it is
// kept and run by Stop after ctx is cancelled.
//
// Both the function and its teardown run under the lifecycle's mutex: Stop and Running wait
// for them, so their work must be bounded. Prefer to spawn goroutines and return; a bounded
// blocking start (a probe with a timeout) is tolerated, an unbounded one is a bug. Neither
// may call back into this lifecycle, and a teardown may wait only for work bound to ctx
// that does not itself block on anything the stop caller holds.
type StartFunc func(ctx context.Context) (teardown func(), err error)

// GenericLifecycle owns one run at a time: Start opens a run under a context derived from
// the parent, Stop cancels it and runs the undo the start returned. Start and Stop are
// idempotent and safe to call from any goroutine: every transition happens under one mutex,
// so Running() only ever reports a completed transition, a Start can never leave a run that
// no Stop can reach, and an owner and the processor it starts switch as one transition.
//
// Stop does not wait for the run's goroutines; it only cancels their context. A run must
// therefore bind everything it owns to that context, or return it in the teardown.
type GenericLifecycle struct {
	name      string
	parentCtx context.Context

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	teardown func()
}

func NewGenericLifecycle(name string, parentCtx context.Context) *GenericLifecycle {
	return &GenericLifecycle{
		name:      name,
		parentCtx: parentCtx,
	}
}

// Start opens a run by calling f with the run's context; see StartFunc for the contract.
// Nothing is started on a cancelled parent: such a run could never do anything, and
// reporting it as running would be a lie.
func (l *GenericLifecycle) Start(f StartFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.running {
		log.Debug().Msgf("lifecycle '%s' is already running", l.name)
		return
	}
	if err := l.parentCtx.Err(); err != nil {
		log.Debug().Err(err).Msgf("parent context of '%s' is closed, not starting", l.name)
		return
	}

	ctx, cancel := context.WithCancel(l.parentCtx)
	teardown, err := f(ctx)
	if err != nil {
		log.Error().Err(err).Msgf("failed to start lifecycle '%s'", l.name)
		cancel()
		if teardown != nil {
			teardown()
		}
		return
	}
	l.running, l.cancel, l.teardown = true, cancel, teardown
}

// Stop cancels the run and then executes the teardown its start returned, both under the
// mutex and only if the lifecycle was running.
func (l *GenericLifecycle) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.running {
		log.Debug().Msgf("lifecycle '%s' is already stopped", l.name)
		return
	}
	l.running = false
	l.cancel()
	if l.teardown != nil {
		l.teardown()
	}
	l.cancel, l.teardown = nil, nil
}

func (l *GenericLifecycle) Running() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

func (l *GenericLifecycle) GetParentContext() context.Context {
	return l.parentCtx
}
