package utils

import (
	"context"
	"sync/atomic"

	"github.com/rs/zerolog/log"
)

type Lifecycle interface {
	Start()
	Stop()
	Running() bool
}

type GenericLifecycle struct {
	name       string
	running    atomic.Bool
	parentCtx  context.Context
	cancelFunc *Atomic[context.CancelFunc]
}

func NewGenericLifecycle(name string, parentCtx context.Context) *GenericLifecycle {
	return &GenericLifecycle{
		name:       name,
		parentCtx:  parentCtx,
		cancelFunc: NewAtomic[context.CancelFunc](),
	}
}

func (l *GenericLifecycle) Start(f func(ctx context.Context) error) {
	if l.running.CompareAndSwap(false, true) {
		if l.parentCtx.Err() != nil {
			log.Error().Err(l.parentCtx.Err()).Msgf("parent context of '%s' is closed", l.name)
		}
		newCtx, cancel := context.WithCancel(l.parentCtx)
		l.cancelFunc.Store(cancel)
		err := f(newCtx)
		if err != nil {
			log.Error().Err(err).Msgf("failed to start lifecycle '%s'", l.name)
			// release whatever the failed start left bound to the context (an
			// opened stream, goroutines) - Stop() will not run for a lifecycle
			// that never became running
			cancel()
			l.running.Store(false)
		}
	} else {
		log.Info().Msgf("lifecycle '%s' is already running", l.name)
	}
}

func (l *GenericLifecycle) Stop() {
	if l.running.CompareAndSwap(true, false) {
		if l.cancelFunc.Load() != nil {
			l.cancelFunc.Load()()
		}
	} else {
		log.Info().Msgf("lifecycle '%s' is already stopped", l.name)
	}
}

func (l *GenericLifecycle) Running() bool {
	return l.running.Load()
}

func (l *GenericLifecycle) GetParentContext() context.Context {
	return l.parentCtx
}
