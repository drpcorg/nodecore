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

type BaseLifecycle struct {
	name       string
	running    atomic.Bool
	parentCtx  context.Context
	cancelFunc *Atomic[context.CancelFunc]
}

func NewBaseLifecycle(name string, parentCtx context.Context) *BaseLifecycle {
	return &BaseLifecycle{
		name:       name,
		parentCtx:  parentCtx,
		cancelFunc: NewAtomic[context.CancelFunc](),
	}
}

func (l *BaseLifecycle) Start(f func(ctx context.Context) error) {
	if l.running.CompareAndSwap(false, true) {
		if l.parentCtx.Err() != nil {
			log.Warn().Err(l.parentCtx.Err()).Msg("parent context is closed")
		}
		newCtx, cancel := context.WithCancel(l.parentCtx)
		l.cancelFunc.Store(cancel)
		err := f(newCtx)
		if err != nil {
			log.Warn().Err(err).Msgf("failed to start lifecycle '%s'", l.name)
			l.running.Store(false)
		}
	} else {
		log.Info().Msgf("lifecycle '%s' is already running", l.name)
	}
}

func (l *BaseLifecycle) Stop() {
	if l.running.CompareAndSwap(true, false) && l.cancelFunc.Load() != nil {
		l.cancelFunc.Load()()
	} else {
		log.Info().Msgf("lifecycle '%s' is already stopped", l.name)
	}
}

func (l *BaseLifecycle) Running() bool {
	return l.running.Load()
}

func (l *BaseLifecycle) GetParentContext() context.Context {
	return l.parentCtx
}
