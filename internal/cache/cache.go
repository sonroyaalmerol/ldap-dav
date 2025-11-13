package cache

import (
	"sync"
	"time"
)

type entry[V any] struct {
	val V
	exp int64 // Unix timestamp in nanoseconds - smaller and faster than time.Time
}

type Cache[K comparable, V any] struct {
	mu      sync.RWMutex
	data    map[K]entry[V]
	ttl     time.Duration
	closeCh chan struct{}
	once    sync.Once
}

func New[K comparable, V any](ttl time.Duration) *Cache[K, V] {
	c := &Cache[K, V]{
		data:    make(map[K]entry[V]),
		ttl:     ttl,
		closeCh: make(chan struct{}),
	}
	go c.cleanupExpired()
	return c
}

func (c *Cache[K, V]) Get(k K) (V, bool) {
	c.mu.RLock()
	e, ok := c.data[k]
	c.mu.RUnlock() // Unlock early

	if !ok || time.Now().UnixNano() > e.exp {
		var zero V
		return zero, false
	}
	return e.val, true
}

func (c *Cache[K, V]) Set(k K, v V) {
	exp := time.Now().Add(c.ttl).UnixNano()
	c.mu.Lock()
	c.data[k] = entry[V]{val: v, exp: exp}
	c.mu.Unlock()
}

func (c *Cache[K, V]) SetWithExpiry(k K, v V, exp time.Time) {
	c.mu.Lock()
	c.data[k] = entry[V]{val: v, exp: exp.UnixNano()}
	c.mu.Unlock()
}

func (c *Cache[K, V]) Delete(k K) bool {
	c.mu.Lock()
	_, ok := c.data[k]
	if ok {
		delete(c.data, k)
	}
	c.mu.Unlock()
	return ok
}

// cleanupExpired periodically removes expired entries to prevent memory leaks
func (c *Cache[K, V]) cleanupExpired() {
	ticker := time.NewTicker(c.ttl)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now().UnixNano()
			c.mu.Lock()
			for k, e := range c.data {
				if now > e.exp {
					delete(c.data, k)
				}
			}
			c.mu.Unlock()
		case <-c.closeCh:
			return
		}
	}
}

// Close stops the background cleanup goroutine
func (c *Cache[K, V]) Close() {
	c.once.Do(func() {
		close(c.closeCh)
	})
}

// Clear removes all entries
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	c.data = make(map[K]entry[V])
	c.mu.Unlock()
}

// Len returns the number of entries (including expired)
func (c *Cache[K, V]) Len() int {
	c.mu.RLock()
	n := len(c.data)
	c.mu.RUnlock()
	return n
}
