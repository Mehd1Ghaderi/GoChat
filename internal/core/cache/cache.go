// Package cache is a sharded, size-bounded map whose entries expire.
package cache

import (
	"hash/maphash"
	"sync"
	"time"
)

const shards = 32

type entry[V any] struct {
	value   V
	expires time.Time
}

type shard[K comparable, V any] struct {
	mu sync.Mutex
	m  map[K]entry[V]
}

type Cache[K comparable, V any] struct {
	seed     maphash.Seed
	ttl      time.Duration
	perShard int
	shards   [shards]shard[K, V]
}

// New returns a cache holding about capacity entries for ttl each.
func New[K comparable, V any](capacity int, ttl time.Duration) *Cache[K, V] {
	c := &Cache[K, V]{seed: maphash.MakeSeed(), ttl: ttl, perShard: max(1, capacity/shards)}
	for i := range c.shards {
		c.shards[i].m = make(map[K]entry[V])
	}
	return c
}

func (c *Cache[K, V]) shard(k K) *shard[K, V] {
	return &c.shards[maphash.Comparable(c.seed, k)%shards]
}

func (c *Cache[K, V]) Get(k K) (V, bool) {
	s := c.shard(k)
	s.mu.Lock()
	e, ok := s.m[k]
	if ok && time.Now().After(e.expires) {
		delete(s.m, k)
		ok = false
	}
	s.mu.Unlock()
	return e.value, ok
}

func (c *Cache[K, V]) Set(k K, v V) {
	c.SetUntil(k, v, time.Now().Add(c.ttl))
}

// SetUntil caches v until the earlier of expires and the cache's ttl.
func (c *Cache[K, V]) SetUntil(k K, v V, expires time.Time) {
	now := time.Now()
	if limit := now.Add(c.ttl); expires.After(limit) {
		expires = limit
	}
	s := c.shard(k)
	s.mu.Lock()
	if len(s.m) >= c.perShard {
		for key, e := range s.m {
			if now.After(e.expires) {
				delete(s.m, key)
			}
		}
		// Still full: drop an arbitrary entry; map order is random.
		for key := range s.m {
			if len(s.m) < c.perShard {
				break
			}
			delete(s.m, key)
		}
	}
	s.m[k] = entry[V]{value: v, expires: expires}
	s.mu.Unlock()
}

func (c *Cache[K, V]) Delete(k K) {
	s := c.shard(k)
	s.mu.Lock()
	delete(s.m, k)
	s.mu.Unlock()
}

// DeleteFunc removes every entry for which drop returns true.
func (c *Cache[K, V]) DeleteFunc(drop func(K, V) bool) {
	for i := range c.shards {
		s := &c.shards[i]
		s.mu.Lock()
		for k, e := range s.m {
			if drop(k, e.value) {
				delete(s.m, k)
			}
		}
		s.mu.Unlock()
	}
}
