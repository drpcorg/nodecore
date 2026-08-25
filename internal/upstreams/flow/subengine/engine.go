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
// Events carries upstream messages plus a terminal frame - a SubResponse with a
// non-nil error on disconnect/failure, or an end frame (IsEnd) when a bounded
// stream completes; the channel is closed when the source ends. A close with
// no terminal frame is reported as a total failure. Stop releases the underlying resources (node unsubscribe, goroutines).
type Source struct {
	Events <-chan protocol.SubResponse
	Stop   func()
	// Buffer optionally overrides the per-subscriber fan-out buffer size for this
	// source. Zero means subscriberBufferSize. A source that bursts many events
	// per upstream message (e.g. all logs of one block) sets a larger value so a
	// momentarily-busy client is not disconnected as "too slow".
	Buffer int
	// Exclusive marks a source that is never shared (its key is unique per
	// request, e.g. a pass-through gRPC stream): the engine tears it down as soon
	// as its subscriber leaves instead of keeping it for the reuse grace period.
	Exclusive bool
}

// SourceBuilder starts a Source bound to srcCtx. srcCtx is cancelled by the
// engine when the source is torn down.
type SourceBuilder func(srcCtx context.Context) (*Source, error)

// Engine aggregates subscriptions for a single chain. Consumers depend on this
// interface; the concrete implementation is genericEngine.
type Engine interface {
	// Subscribe attaches a subscriber to the shared source identified by key,
	// building it via build on the first subscriber. See genericEngine.Subscribe.
	Subscribe(key string, build SourceBuilder) (*Subscription, error)
}

// subscriber is a single client's view of a shared source, owned entirely by the
// source's actor goroutine.
type subscriber struct {
	id int
	ch chan protocol.SubResponse // data events; the actor closes it to signal terminal
	// terminal is the frame that ended the subscription (nil for a clean end),
	// set by the actor before close(ch) and read by the client after. It keeps
	// the whole frame, not just the error, so transport metadata riding on an
	// upstream error frame (gRPC trailers) survives the fan-out.
	terminal protocol.SubResponse
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
	Events <-chan protocol.SubResponse

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
// for a clean detach or a clean end (see Terminal) and a *protocol.ResponseError
// when the source died or this subscriber was disconnected for lagging. Reading is safe without a lock: the
// actor writes sub.terminal before closing the channel, and the client reads it only
// after observing the close (which establishes the happens-before edge).
func (s *Subscription) Err() *protocol.ResponseError {
	if s.sub.terminal == nil {
		return nil
	}
	return s.sub.terminal.GetError()
}

// Terminal returns the frame that ended the subscription - an error frame, or
// an end frame (IsEnd) when a bounded stream completed - so consumers can
// forward the metadata it carries. nil after a plain detach. Same reading rules
// as Err.
func (s *Subscription) Terminal() protocol.SubResponse {
	return s.sub.terminal
}

// genericEngine is the default Engine implementation.
type genericEngine struct {
	ctx           context.Context
	chain         chains.Chain
	teardownDelay time.Duration

	sources *utils.CMap[string, *sourceActor]
}

var _ Engine = (*genericEngine)(nil)

func NewEngine(ctx context.Context, chain chains.Chain) Engine {
	return &genericEngine{
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
func (e *genericEngine) Subscribe(key string, build SourceBuilder) (*Subscription, error) {
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

func (e *genericEngine) getOrCreate(key string, build SourceBuilder) *sourceActor {
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

func (e *genericEngine) newSubscription(a *sourceActor, sub *subscriber) *Subscription {
	return &Subscription{Events: sub.ch, actor: a, sub: sub}
}

// run is the actor goroutine: it builds the source once, then owns all of the
// key's state inside a single select loop until the source ends.
func (e *genericEngine) run(a *sourceActor, build SourceBuilder) {
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

	var srcEvents <-chan protocol.SubResponse

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
	// the actor returns and no longer accepts subscribers. terminal is the
	// frame that ended the source (error or end frame); nil means the source was
	// released with nobody listening.
	terminate := func(terminal protocol.SubResponse) {
		for _, s := range subs {
			s.terminal = terminal
			close(s.ch)
		}
		e.sources.CompareAndDelete(a.key, a)
		close(a.done)
		cancel()
		src.Stop()
	}

	// subscriberGone is the single rule for a subscriber leaving (detach or
	// too-slow disconnect): an exclusive source is released at once; a shared
	// one without listeners starts its reuse grace period. It reports whether
	// the actor must return.
	subscriberGone := func(s *subscriber) bool {
		delete(subs, s.id)
		refs--
		if refs != 0 {
			return false
		}
		if src.Exclusive {
			// nobody can ever reuse this source - release the upstream now
			terminate(nil)
			return true
		}
		if teardownC == nil {
			arm()
		}
		return false
	}

	for {
		select {
		case <-e.ctx.Done():
			terminate(totalFailureFrame())
			return

		case ev, ok := <-srcEvents:
			if !ok {
				terminate(totalFailureFrame())
				return
			}
			if ev.GetError() != nil || ev.IsEnd() {
				terminate(ev)
				return
			}
			for id, s := range subs {
				select {
				case s.ch <- ev:
				default:
					// Slow consumer: disconnect it (no silent data gaps) rather
					// than dropping the event. Its later Unsubscribe is a no-op.
					s.terminal = &protocol.GenericSubResponse{Error: protocol.SubscriberTooSlowError()}
					close(s.ch)
					log.Warn().Msgf("disconnected lagging subscriber %s/sub-%d: buffer of %d full", a.key, id, bufSize)
					// subscriberGone arms the grace timer only if it isn't already
					// running: events keep arriving with no audience during the
					// teardown window, and re-arming on each one would keep an
					// active source alive indefinitely
					if subscriberGone(s) {
						return
					}
				}
			}

		case cmd := <-a.subscribe:
			disarm() // a resubscribe within the teardown window revives the source
			if srcEvents == nil {
				srcEvents = src.Events // first listener: start draining the source
			}
			seq++
			refs++
			s := &subscriber{id: seq, ch: make(chan protocol.SubResponse, bufSize)}
			subs[s.id] = s
			cmd.reply <- e.newSubscription(a, s)

		case s := <-a.unsubscribe:
			if _, ok := subs[s.id]; ok && subscriberGone(s) {
				return
			}

		case <-teardownC:
			if refs == 0 {
				terminate(nil)
				return
			}
		}
	}
}

// totalFailureFrame is the synthetic terminal frame for a source that ended
// without saying why (node disconnect, engine shutdown).
func totalFailureFrame() protocol.SubResponse {
	return &protocol.GenericSubResponse{Error: protocol.SubscribeTotalFailureError()}
}
