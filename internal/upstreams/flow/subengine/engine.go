// Package subengine implements a per-chain subscription aggregation engine. It
// dedups subscriptions by a caller-provided key so that identical client
// subscriptions share a single upstream source and fan out from one channel,
// instead of opening one node subscription per client.
//
// The engine is producer-agnostic: callers pass a SourceBuilder that knows how
// to start the underlying source (a generic node-backed ws subscription, or -
// in later stages - locally synthesized newHeads/logs). The engine owns
// ref-counting, fan-out and lifecycle (teardown after the last client leaves).
package subengine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
)

// defaultTeardownDelay mirrors dshackle's refCount(1, 60s): the shared source is
// kept alive for this long after the last subscriber leaves, so a quick
// resubscribe reuses it instead of re-opening a node subscription.
const defaultTeardownDelay = 10 * time.Second

// Source is a started, normalized subscription stream that the engine fans out.
// Events carries upstream messages plus, on disconnect/error, a terminal
// *protocol.WsResponse with Error set; the channel is closed when the source
// ends. Stop releases the underlying resources (node unsubscribe, goroutines).
type Source struct {
	Events <-chan *protocol.WsResponse
	Stop   func()
}

// SourceBuilder starts a Source bound to srcCtx. srcCtx is cancelled by the
// engine when the source is torn down.
type SourceBuilder func(srcCtx context.Context) (*Source, error)

type sharedSource struct {
	sm       *utils.SubscriptionManager[*protocol.WsResponse]
	stop     func()
	refs     int
	seq      int
	teardown *time.Timer
	closed   bool
}

// Engine aggregates subscriptions for a single chain. Consumers depend on this
// interface; the concrete implementation is baseEngine.
type Engine interface {
	// Subscribe attaches a subscriber to the shared source identified by key,
	// building it via build on the first subscriber. See baseEngine.Subscribe.
	Subscribe(key string, build SourceBuilder) (<-chan *protocol.WsResponse, func(), error)
}

// baseEngine is the default Engine implementation.
type baseEngine struct {
	ctx           context.Context
	chain         chains.Chain
	sup           upstreams.UpstreamSupervisor
	teardownDelay time.Duration

	mu      sync.Mutex
	sources map[string]*sharedSource
}

var _ Engine = (*baseEngine)(nil)

func NewEngine(ctx context.Context, chain chains.Chain, sup upstreams.UpstreamSupervisor) Engine {
	return &baseEngine{
		ctx:           ctx,
		chain:         chain,
		sup:           sup,
		teardownDelay: defaultTeardownDelay,
		sources:       make(map[string]*sharedSource),
	}
}

// Subscribe attaches a new subscriber to the shared source identified by key,
// building the source via build on the first subscriber. It returns the
// subscriber's event channel, an unsubscribe func to detach, and a build error
// if the source could not be started.
//
// The channel carries subscription events and, on disconnect/error, a terminal
// *protocol.WsResponse with Error set. It never carries subscription
// confirmations: the upstream's own confirmation is swallowed by the source
// builder, and each subscriber allocates its own client-facing subscription id
// in the caller (see SubscriptionRequestProcessor).
func (e *baseEngine) Subscribe(key string, build SourceBuilder) (<-chan *protocol.WsResponse, func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	s, ok := e.sources[key]
	var pumpEvents <-chan *protocol.WsResponse
	if !ok {
		srcCtx, cancel := context.WithCancel(e.ctx)
		src, err := build(srcCtx)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		var stopOnce sync.Once
		s = &sharedSource{
			sm: utils.NewSubscriptionManager[*protocol.WsResponse](fmt.Sprintf("subengine_%s_%s", e.chain, key)),
			stop: func() {
				stopOnce.Do(func() {
					src.Stop()
					cancel()
				})
			},
		}
		e.sources[key] = s
		pumpEvents = src.Events
	}

	if s.teardown != nil {
		s.teardown.Stop()
		s.teardown = nil
	}
	s.refs++
	s.seq++
	sub := s.sm.SubscribeWithSize(fmt.Sprintf("sub-%d", s.seq), 100)

	// Start pumping only once the first subscriber is attached, so a fast
	// source cannot publish (and drop) events before anyone is listening.
	if pumpEvents != nil {
		go e.pump(key, s, pumpEvents)
	}

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			sub.Unsubscribe()
			e.detach(key)
		})
	}
	return sub.Events, unsub, nil
}

// pump forwards every source event to the source's subscribers and, when the
// source ends, removes it from the engine and releases it.
func (e *baseEngine) pump(key string, s *sharedSource, events <-chan *protocol.WsResponse) {
	for ev := range events {
		s.sm.Publish(ev)
	}
	e.mu.Lock()
	if !s.closed {
		s.closed = true
		if s.teardown != nil {
			s.teardown.Stop()
			s.teardown = nil
		}
		if cur, ok := e.sources[key]; ok && cur == s {
			delete(e.sources, key)
		}
	}
	e.mu.Unlock()
	s.stop()
}

// detach decrements the ref count and, when it reaches zero, schedules teardown
// after teardownDelay.
func (e *baseEngine) detach(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sources[key]
	if !ok {
		return
	}
	s.refs--
	if s.refs <= 0 && !s.closed && s.teardown == nil {
		s.teardown = time.AfterFunc(e.teardownDelay, func() { e.teardownSource(key, s) })
	}
}

func (e *baseEngine) teardownSource(key string, s *sharedSource) {
	e.mu.Lock()
	if s.closed || s.refs > 0 {
		e.mu.Unlock()
		return
	}
	s.closed = true
	if cur, ok := e.sources[key]; ok && cur == s {
		delete(e.sources, key)
	}
	e.mu.Unlock()
	s.stop()
}
