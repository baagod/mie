package xsync

import "sync"

type Map[K comparable, V any] struct {
	m sync.Map
}

func (m *Map[K, V]) Load(k K) (v V, ok bool) {
	if a, b := m.m.Load(k); b {
		v, ok = a.(V)
	}
	return
}

func (m *Map[K, V]) LoadOrStore(k K, v V) (V, bool) {
	actual, loaded := m.m.LoadOrStore(k, v)
	return actual.(V), loaded
}
