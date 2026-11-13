package sync

import "sync"

type Map[K comparable, V any] struct {
    m sync.Map
}

func (m *Map[K, V]) Load(k K) (v V, ok bool) {
    if a, b := m.m.Load(k); b {
        if v, ok = a.(V); ok {
            return
        }
    }
    return v, ok
}

func (m *Map[K, V]) Store(k K, v V) {
    m.m.Store(k, v)
}

func (m *Map[K, V]) Delete(k K) {
    m.m.Delete(k)
}

func (m *Map[K, V]) Clear() {
    m.m.Clear()
}

func (m *Map[K, V]) LoadOrStore(k K, v V) (V, bool) {
    actual, loaded := m.m.LoadOrStore(k, v)
    return actual.(V), loaded
}

func (m *Map[K, V]) LoadAndDelete(k K) (v V, ok bool) {
    actual, loaded := m.m.LoadAndDelete(k)
    return actual.(V), loaded
}

func (m *Map[K, V]) Swap(k K, v V) {
    m.m.Swap(k, v)
}

func (m *Map[K, V]) Range(f func(K, V) bool) {
    m.m.Range(func(k, v any) bool {
        return f(k.(K), v.(V))
    })
}

func (m *Map[K, V]) CompareAndSwap(k K, old, new V) (swapped bool) {
    return m.m.CompareAndSwap(k, old, new)
}

func (m *Map[K, V]) CompareAndDelete(k K, old V) (deleted bool) {
    return m.m.CompareAndDelete(k, old)
}
