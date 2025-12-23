package utils

import (
	"sync/atomic"
	"time"

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
	closed  atomic.Bool
}

func (s *Subscription[T]) Unsubscribe() {
	s.closed.Store(true)
	s.manager.subscriptions.Delete(s.name)
	numSubscriptions.WithLabelValues(s.manager.name).Dec()
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(s.Events)
	}()
}

type SubscriptionManager[T any] struct {
	subscriptions CMap[string, *Subscription[T]]
	name          string
}

func (sm *SubscriptionManager[T]) Subscribe(name string) *Subscription[T] {
	return sm.SubscribeWithSize(name, 100)
}

func (sm *SubscriptionManager[T]) SubscribeWithSize(name string, size int) *Subscription[T] {
	sub := &Subscription[T]{
		name:    name,
		Events:  make(chan T, size),
		manager: sm,
		closed:  atomic.Bool{},
	}
	_, loaded := sm.subscriptions.LoadOrStore(name, sub)
	if loaded {
		log.Panic().Msgf("Fatal error - %s subscription already exists at %s", name, sm.name)
	}
	numSubscriptions.WithLabelValues(sm.name).Inc()
	return sub
}

func (sm *SubscriptionManager[T]) SubscribeWithInitialState(name string, size int, initialState T) *Subscription[T] {
	sub := &Subscription[T]{
		name:    name,
		Events:  make(chan T, size),
		manager: sm,
		closed:  atomic.Bool{},
	}
	sub.Events <- initialState

	_, loaded := sm.subscriptions.LoadOrStore(name, sub)
	if loaded {
		log.Panic().Msgf("Fatal error - %s subscription already exists at %s", name, sm.name)
	}
	numSubscriptions.WithLabelValues(sm.name).Inc()
	return sub
}

func (sm *SubscriptionManager[T]) Publish(event T) {
	sm.subscriptions.Range(func(key string, sub *Subscription[T]) bool {
		if sub.closed.Load() {
			return true
		}
		unreadMessages.WithLabelValues(sub.name).Set(float64(len(sub.Events)))
		select {
		case sub.Events <- event:
			// success
		default:
			log.Error().Msgf("Publish event %v from %v to %v subs failed", event, sm.name, sub.name)
		}

		return true
	})
	publishRate.WithLabelValues(sm.name).Inc()
}

func NewSubscriptionManager[T any](name string) *SubscriptionManager[T] {
	return &SubscriptionManager[T]{
		subscriptions: CMap[string, *Subscription[T]]{},
		name:          name,
	}
}
