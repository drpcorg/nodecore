package utils

import (
	"sync"
	"sync/atomic"
)

type Atomic[T any] struct {
	v atomic.Value
}

func NewAtomic[T any]() *Atomic[T] {
	var v T
	a := Atomic[T]{}
	a.Store(v)
	return &a
}

func (a *Atomic[T]) Store(val T) {
	a.v.Store(val)
}

func (a *Atomic[T]) Has() bool {
	return a.v.Load() != nil
}

func (a *Atomic[T]) Load() T {
	return a.v.Load().(T)
}

func (a *Atomic[T]) CompareAndSwap(old T, new T) bool {
	return a.v.CompareAndSwap(old, new)
}

type CMap[K any, V any] struct {
	mp sync.Map
}

func NewCMap[K any, V any]() *CMap[K, V] {
	return &CMap[K, V]{}
}

func (mp *CMap[K, V]) Load(key K) (V, bool) {
	var zero V
	res, loaded := mp.mp.Load(key)
	if !loaded {
		return zero, false
	}
	v := res.(V)
	return v, true
}

func (mp *CMap[K, V]) LoadOrStore(key K, val V) (V, bool) {
	loadedval, loaded := mp.mp.LoadOrStore(key, val)
	return loadedval.(V), loaded
}

// LoadOrStoreLazy returns the existing value for the key if present. Otherwise
// it calls valueFn to build a value, stores it, and returns it. valueFn is only
// invoked on a miss, so callers on the common (already-present) path never pay
// the construction cost. The returned bool reports whether the value was loaded
// (true) rather than built and stored by this call (false).
//
// Note: under a first-time race on the same key, valueFn may be evaluated by
// more than one goroutine; only one built value is kept and all callers observe
// it. This is inherent to sync.Map's lack of a lazy primitive and is acceptable
// because it is bounded and one-time - the steady-state hit never calls valueFn.
func (mp *CMap[K, V]) LoadOrStoreLazy(key K, valueFn func() V) (V, bool) {
	if v, ok := mp.mp.Load(key); ok {
		return v.(V), true
	}
	loadedval, loaded := mp.mp.LoadOrStore(key, valueFn())
	return loadedval.(V), loaded
}

func (mp *CMap[K, V]) LoadAndDelete(key K) (V, bool) {
	var zero V
	v, loaded := mp.mp.LoadAndDelete(key)
	if !loaded {
		return zero, false
	}
	return v.(V), loaded
}

func (mp *CMap[K, V]) Range(f func(key K, val V) bool) {
	mp.mp.Range(func(k, v any) bool {
		return f(k.(K), v.(V))
	})
}

func (mp *CMap[K, V]) Store(key K, val V) {
	mp.mp.Store(key, val)
}

func (mp *CMap[K, V]) Delete(key K) {
	mp.mp.Delete(key)
}

func (mp *CMap[K, V]) CompareAndSwap(key K, old V, new V) bool {
	return mp.mp.CompareAndSwap(key, old, new)
}

func (mp *CMap[K, V]) CompareAndDelete(key K, old V) bool {
	return mp.mp.CompareAndDelete(key, old)
}
