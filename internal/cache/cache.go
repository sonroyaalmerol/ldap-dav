package cache

import (
	"sync"
	"time"
)

type entry[V any] struct {
	val V
	exp time.Time
}

type Cache[K comparable, V any] struct {
	mu   sync.RWMutex
	data map[K]entry[V]
	ttl  time.Duration
}

func New[K comparable, V any](ttl time.Duration) *Cache[K, V] {
	return &Cache[K, V]{data: make(map[K]entry[V]), ttl: ttl}
}

func (c *Cache[K, V]) Get(k K) (V, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.data[k]
	if !ok || time.Now().After(e.exp) {
		var zero V
		return zero, false
	}
	return e.val, true
}

func (c *Cache[K, V]) Set(k K, v V, exp time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[k] = entry[V]{val: v, exp: exp}
}
