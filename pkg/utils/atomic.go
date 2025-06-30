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

func (mp *CMap[K, V]) Load(key K) (*V, bool) {
	res, loaded := mp.mp.Load(key)
	if !loaded {
		return nil, false
	}
	v := res.(*V)
	return v, true
}

func (mp *CMap[K, V]) LoadOrStore(key K, val *V) (*V, bool) {
	loadedval, loaded := mp.mp.LoadOrStore(key, val)
	return loadedval.(*V), loaded
}

func (mp *CMap[K, V]) LoadAndDelete(key K) (*V, bool) {
	v, loaded := mp.mp.LoadAndDelete(key)
	if !loaded {
		return nil, false
	}
	return v.(*V), loaded
}

func (mp *CMap[K, V]) Range(f func(key K, val *V) bool) {
	mp.mp.Range(func(k, v any) bool {
		return f(k.(K), v.(*V))
	})
}

func (mp *CMap[K, V]) Store(key K, val *V) {
	mp.mp.Store(key, val)
}

func (mp *CMap[K, V]) Delete(key K) {
	mp.mp.Delete(key)
}

func (mp *CMap[K, V]) CompareAndSwap(key K, old *V, new *V) bool {
	return mp.mp.CompareAndSwap(key, old, new)
}
