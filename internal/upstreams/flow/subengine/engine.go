// Package subengine implements a per-chain subscription aggregation engine. It
// dedups subscriptions by a caller-provided key so that identical client
// subscriptions share a single upstream source and fan out from one channel,
// instead of opening one node subscription per client.
//
// The engine is producer-agnostic: callers pass a SourceBuilder that knows how
// to start the underlying source (a generic node-backed ws subscription, or
// locally synthesized newHeads/logs).
//
// Design: each aggregation key is owned by a single goroutine (a sourceActor).
// That goroutine holds ALL of the key's mutable state - subscribers, ref count,
// teardown timer, terminal cause - as ordinary local variables, and is the only
// thing that ever touches them. There are therefore no mutexes: concurrency is
// resolved by channel sends to the actor, not by locking shared state. The
// process-wide key -> actor map is the lock-free utils.CMap.
//
// Terminal state is delivered out of band: a subscriber learns the source ended
// only by its Events channel being closed, after which Subscription.Err returns
// the cause. The channel never carries an in-band error frame, so a terminal
// signal can never be lost to a full buffer or missed by a late subscriber.
package subengine

import (
	"cmp"
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

// defaultTeardownDelay mirrors dshackle's refCount(1, 60s): the shared source is
// kept alive for this long after the last subscriber leaves, so a quick
// resubscribe reuses it instead of re-opening a node subscription.
const defaultTeardownDelay = 10 * time.Second

// subscriberBufferSize bounds how far a single subscriber may lag behind the
// shared source before it is disconnected (see the fan-out in run).
const subscriberBufferSize = 100

// Source is a started, normalized subscription stream that the engine fans out.
// Events carries upstream messages plus, on disconnect/error, a terminal
// *protocol.WsResponse with Error set; the channel is closed when the source
// ends. Stop releases the underlying resources (node unsubscribe, goroutines).
type Source struct {
	Events <-chan *protocol.WsResponse
	Stop   func()
	// Buffer optionally overrides the per-subscriber fan-out buffer size for this
	// source. Zero means subscriberBufferSize. A source that bursts many events
	// per upstream message (e.g. all logs of one block) sets a larger value so a
	// momentarily-busy client is not disconnected as "too slow".
	Buffer int
}

// SourceBuilder starts a Source bound to srcCtx. srcCtx is cancelled by the
// engine when the source is torn down.
type SourceBuilder func(srcCtx context.Context) (*Source, error)

// Engine aggregates subscriptions for a single chain. Consumers depend on this
// interface; the concrete implementation is baseEngine.
type Engine interface {
	// Subscribe attaches a subscriber to the shared source identified by key,
	// building it via build on the first subscriber. See baseEngine.Subscribe.
	Subscribe(key string, build SourceBuilder) (*Subscription, error)
}

// subscriber is a single client's view of a shared source, owned entirely by the
// source's actor goroutine.
type subscriber struct {
	id  int
	ch  chan *protocol.WsResponse // data events; the actor closes it to signal terminal
	err *protocol.ResponseError   // set by the actor before close(ch); read by the client after
}

// subscribeCmd asks the actor to attach a new subscriber and reply with its
// Subscription.
type subscribeCmd struct {
	reply chan *Subscription
}

// sourceActor is the handle other goroutines use to talk to a key's owning
// goroutine. All of its fields are immutable after creation except buildErr,
// which is written once before done is closed and read only after done.
type sourceActor struct {
	key         string
	subscribe   chan subscribeCmd
	unsubscribe chan *subscriber
	done        chan struct{} // closed when the actor stops accepting subscribers
	buildErr    error         // write-once before close(done); valid to read after <-done
}

// Subscription is a client's handle to a shared source.
type Subscription struct {
	Events <-chan *protocol.WsResponse

	actor *sourceActor
	sub   *subscriber
}

// Unsubscribe detaches this subscriber from the shared source. It is a guarded
// send to the owning actor; the actor treats a repeated or unknown detach as a
// no-op, so it is safe to call more than once.
func (s *Subscription) Unsubscribe() {
	select {
	case s.actor.unsubscribe <- s.sub:
	case <-s.actor.done: // actor already gone - nothing to detach from
	}
}

// Err returns the terminal cause after Events has been closed. It returns nil
// for a clean detach and a *protocol.ResponseError when the source died or this
// subscriber was disconnected for lagging. Reading is safe without a lock: the
// actor writes sub.err before closing the channel, and the client reads it only
// after observing the close (which establishes the happens-before edge).
func (s *Subscription) Err() *protocol.ResponseError {
	return s.sub.err
}

// baseEngine is the default Engine implementation.
type baseEngine struct {
	ctx           context.Context
	chain         chains.Chain
	teardownDelay time.Duration

	sources *utils.CMap[string, *sourceActor]
}

var _ Engine = (*baseEngine)(nil)

func NewEngine(ctx context.Context, chain chains.Chain) Engine {
	return &baseEngine{
		ctx:           ctx,
		chain:         chain,
		teardownDelay: defaultTeardownDelay,
		sources:       utils.NewCMap[string, *sourceActor](),
	}
}

// Subscribe attaches a new subscriber to the shared source identified by key,
// building the source via build on the first subscriber. It returns a
// Subscription whose Events channel carries data events and is closed on
// termination (the cause is then available via Subscription.Err), or a build
// error if the source could not be started.
//
// The event channel never carries subscription confirmations: the upstream's
// own confirmation is swallowed by the source builder, and each subscriber
// allocates its own client-facing subscription id in the caller (see
// SubscriptionRequestProcessor).
func (e *baseEngine) Subscribe(key string, build SourceBuilder) (*Subscription, error) {
	for {
		a := e.getOrCreate(key, build)
		reply := make(chan *Subscription, 1)
		select {
		case a.subscribe <- subscribeCmd{reply: reply}:
			return <-reply, nil
		case <-a.done:
			// The actor stopped accepting subscribers. If the build failed, the
			// error is definitive - return it (never spin retrying). Otherwise
			// the source was torn down or terminated, and this new caller should
			// transparently build a fresh one.
			if a.buildErr != nil {
				return nil, a.buildErr
			}
		}
	}
}

func (e *baseEngine) getOrCreate(key string, build SourceBuilder) *sourceActor {
	if a, ok := e.sources.Load(key); ok {
		return a
	}
	a := &sourceActor{
		key:         key,
		subscribe:   make(chan subscribeCmd),
		unsubscribe: make(chan *subscriber),
		done:        make(chan struct{}),
	}
	if actual, loaded := e.sources.LoadOrStore(key, a); loaded {
		return actual // lost the create race; our actor was never started
	}
	go e.run(a, build)
	return a
}

func (e *baseEngine) newSubscription(a *sourceActor, sub *subscriber) *Subscription {
	return &Subscription{Events: sub.ch, actor: a, sub: sub}
}

// run is the actor goroutine: it builds the source once, then owns all of the
// key's state inside a single select loop until the source ends.
func (e *baseEngine) run(a *sourceActor, build SourceBuilder) {
	srcCtx, cancel := context.WithCancel(e.ctx)
	src, err := build(srcCtx)
	if err != nil {
		a.buildErr = err
		e.sources.CompareAndDelete(a.key, a)
		close(a.done)
		cancel()
		return
	}

	bufSize := cmp.Or(src.Buffer, subscriberBufferSize)

	subs := make(map[int]*subscriber)
	var seq, refs int
	var teardown *time.Timer
	var teardownC <-chan time.Time // nil unless armed; a nil channel never fires

	var srcEvents <-chan *protocol.WsResponse

	// arm starts the teardown grace timer: once
	// the last subscriber has left, the source is kept alive for teardownDelay so
	// a quick resubscribe can reuse it. When the timer fires, the <-teardownC case
	// tears the source down. teardownC mirrors the timer's channel so the select
	// can wait on it; while disarmed it is nil and that select case never fires.
	arm := func() {
		teardown = time.NewTimer(e.teardownDelay)
		teardownC = teardown.C
	}
	// disarm cancels a running grace timer - called when a subscriber attaches, so
	// a source with live subscribers is never torn down. Safe to call when no
	// timer is armed (no-op).
	disarm := func() {
		if teardown != nil {
			teardown.Stop()
			teardown, teardownC = nil, nil
		}
	}

	// terminate closes every subscriber out of band (cause first, then close so
	// the client reads it), removes the source, and releases it. After it runs
	// the actor returns and no longer accepts subscribers.
	terminate := func(cause *protocol.ResponseError) {
		if cause == nil {
			cause = protocol.WsTotalFailureError()
		}
		for _, s := range subs {
			s.err = cause
			close(s.ch)
		}
		e.sources.CompareAndDelete(a.key, a)
		close(a.done)
		cancel()
		src.Stop()
	}

	for {
		select {
		case <-e.ctx.Done():
			terminate(protocol.WsTotalFailureError())
			return

		case ev, ok := <-srcEvents:
			if !ok {
				terminate(nil)
				return
			}
			if ev.Error != nil {
				terminate(ev.Error)
				return
			}
			for id, s := range subs {
				select {
				case s.ch <- ev:
				default:
					// Slow consumer: disconnect it (no silent data gaps) rather
					// than dropping the event. Its later Unsubscribe is a no-op.
					s.err = protocol.WsSubscriberTooSlowError()
					close(s.ch)
					delete(subs, id)
					refs--
					log.Warn().Msgf("disconnected lagging subscriber %s/sub-%d: buffer of %d full", a.key, id, bufSize)
				}
			}
			if refs == 0 && teardownC == nil {
				// Arm the grace timer only if it isn't already running. Events keep
				// arriving with no audience during the teardown window; re-arming on
				// each one would reset the window indefinitely and a source on an
				// active chain (event interval < teardownDelay) would never tear down.
				arm()
			}

		case cmd := <-a.subscribe:
			disarm() // a resubscribe within the teardown window revives the source
			if srcEvents == nil {
				srcEvents = src.Events // first listener: start draining the source
			}
			seq++
			refs++
			s := &subscriber{id: seq, ch: make(chan *protocol.WsResponse, bufSize)}
			subs[s.id] = s
			cmd.reply <- e.newSubscription(a, s)

		case s := <-a.unsubscribe:
			if _, ok := subs[s.id]; ok {
				delete(subs, s.id)
				refs--
				if refs == 0 && teardownC == nil {
					arm()
				}
			}

		case <-teardownC:
			if refs == 0 {
				terminate(nil)
				return
			}
		}
	}
}
