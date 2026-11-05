package utils

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

var publishRate = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "chanutil",
	Subsystem: "subscriptions",
	Name:      "rate_events",
}, []string{"source"})

var numSubscriptions = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "chanutil",
	Subsystem: "subscriptions",
	Name:      "num",
}, []string{"source"})

// unreadMessages is labelled by subscriber name and holds the largest backlog among the
// subscribers sharing that name, sampled on every Publish. The series is removed when the
// last subscriber of that name leaves, so a consumer that left with a backlog does not look
// slow forever. Its cardinality is bounded only as long as names are static - see
// SubscriptionManager.Subscribe.
var unreadMessages = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "chanutil",
	Subsystem: "unread_messages",
	Name:      "num",
}, []string{"source"})

func init() {
	prometheus.MustRegister(
		publishRate,
		numSubscriptions,
		unreadMessages,
	)
}

type Subscription[T any] struct {
	Events  chan T
	name    string
	manager *SubscriptionManager[T]
}

// Unsubscribe removes the subscription from its manager and closes Events. Idempotent.
func (s *Subscription[T]) Unsubscribe() {
	s.manager.unsubscribe(s)
}

// SubscriptionManager fans events out to its subscribers. A subscription is identified by
// its pointer; its name is a label for logs and metrics and may be shared, so a consumer
// that restarts can subscribe again under the same name while its previous subscription is
// still being released.
type SubscriptionManager[T any] struct {
	name string
	// mu guards every field below. Holding it across the whole of Publish makes registration
	// and removal atomic with respect to delivery: a subscriber joins or leaves entirely
	// before or entirely after a publish, never in the middle of one. That is what lets
	// unsubscribe close Events without a per-subscription lock - no send can be in flight -
	// and what keeps a replay seed correct: without it a replay subscriber could snapshot the
	// last state, have a Publish run (and miss it, being unregistered), then register -
	// ending up permanently stale.
	mu            sync.Mutex
	subscriptions map[*Subscription[T]]struct{}
	// subscribersByName counts live subscribers per name; a name's unread series is
	// removed together with its last subscriber.
	subscribersByName map[string]int
	// unreadByName is Publish's scratch space, reused across publishes.
	unreadByName map[string]int
	lastEvent    T
	hasLast      bool
}

// Subscribe registers a subscriber under name. The name must be static - built from
// configuration or code, never from a request, a client or a uuid: it becomes a metric
// label, and a series lives as long as any subscriber of that name does.
func (sm *SubscriptionManager[T]) Subscribe(name string) *Subscription[T] {
	return sm.SubscribeWithSize(name, 100)
}

func (sm *SubscriptionManager[T]) SubscribeWithSize(name string, size int) *Subscription[T] {
	sub := sm.newSubscription(name, size)
	sm.mu.Lock()
	sm.register(sub)
	sm.mu.Unlock()
	return sub
}

// SubscribeWithReplay registers a subscriber and, if any event has been published
// before, seeds it with the most recent one. The snapshot-and-register is done under
// sm.mu (the same lock Publish holds), so it is atomic with respect to a concurrent
// Publish: the subscriber either sees a publish that happened entirely before it
// registered (replayed as the seed) or one that happened entirely after (delivered
// normally) - never a publish that slips through the gap. Use this for state-style
// streams where a late subscriber must learn the current state. The name rule of
// Subscribe applies.
func (sm *SubscriptionManager[T]) SubscribeWithReplay(name string) *Subscription[T] {
	return sm.SubscribeWithReplaySize(name, 100)
}

func (sm *SubscriptionManager[T]) SubscribeWithReplaySize(name string, size int) *Subscription[T] {
	sub := sm.newSubscription(name, size)
	sm.mu.Lock()
	if sm.hasLast {
		sub.Events <- sm.lastEvent
	}
	sm.register(sub)
	sm.mu.Unlock()
	return sub
}

func (sm *SubscriptionManager[T]) Publish(event T) {
	sm.mu.Lock()
	sm.lastEvent = event
	sm.hasLast = true
	clear(sm.unreadByName)
	for sub := range sm.subscriptions {
		sm.deliver(sub, event)
	}
	for name, unread := range sm.unreadByName {
		unreadMessages.WithLabelValues(name).Set(float64(unread))
	}
	sm.mu.Unlock()
	publishRate.WithLabelValues(sm.name).Inc()
}

func NewSubscriptionManager[T any](name string) *SubscriptionManager[T] {
	return &SubscriptionManager[T]{
		subscriptions:     make(map[*Subscription[T]]struct{}),
		subscribersByName: make(map[string]int),
		unreadByName:      make(map[string]int),
		name:              name,
	}
}

func (sm *SubscriptionManager[T]) newSubscription(name string, size int) *Subscription[T] {
	return &Subscription[T]{
		name:    name,
		Events:  make(chan T, size),
		manager: sm,
	}
}

// register adds sub to the live set. Called with sm.mu held.
func (sm *SubscriptionManager[T]) register(sub *Subscription[T]) {
	sm.subscriptions[sub] = struct{}{}
	sm.subscribersByName[sub.name]++
	numSubscriptions.WithLabelValues(sm.name).Inc()
}

// unsubscribe removes sub and closes its channel. Idempotent: a subscription that is no
// longer in the live set has already been released.
func (sm *SubscriptionManager[T]) unsubscribe(sub *Subscription[T]) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if _, live := sm.subscriptions[sub]; !live {
		return
	}
	delete(sm.subscriptions, sub)
	close(sub.Events)
	numSubscriptions.WithLabelValues(sm.name).Dec()

	sm.subscribersByName[sub.name]--
	if sm.subscribersByName[sub.name] == 0 {
		delete(sm.subscribersByName, sub.name)
		unreadMessages.DeleteLabelValues(sub.name)
	}
}

// deliver hands event to one subscriber without blocking and records its backlog under its
// name. Called with sm.mu held.
func (sm *SubscriptionManager[T]) deliver(sub *Subscription[T], event T) {
	unread := len(sub.Events)
	if worst, seen := sm.unreadByName[sub.name]; !seen || unread > worst {
		sm.unreadByName[sub.name] = unread
	}
	select {
	case sub.Events <- event:
		// success
	default:
		log.Error().Msgf("Publish event %v from %v to %v subs failed", event, sm.name, sub.name)
	}
}
