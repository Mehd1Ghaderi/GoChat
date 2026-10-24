// Package cache is a sharded, size-bounded map whose entries expire.
package cache

import (
	"hash/maphash"
	"sync"
	"time"
)

const shards = 32

type entry[K comparable, V any] struct {
	key        K
	value      V
	expires    time.Time
	prev, next *entry[K, V]
}

// shard keeps its entries in the order they were set, so a full shard drops
// the oldest in constant time.
type shard[K comparable, V any] struct {
	mu         sync.Mutex
	m          map[K]*entry[K, V]
	head, tail *entry[K, V]
}

func (s *shard[K, V]) unlink(e *entry[K, V]) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		s.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		s.tail = e.prev
	}
	e.prev, e.next = nil, nil
}

func (s *shard[K, V]) push(e *entry[K, V]) {
	e.prev = s.tail
	if s.tail != nil {
		s.tail.next = e
	} else {
		s.head = e
	}
	s.tail = e
}

func (s *shard[K, V]) remove(e *entry[K, V]) {
	s.unlink(e)
	delete(s.m, e.key)
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
		c.shards[i].m = make(map[K]*entry[K, V])
	}
	return c
}

func (c *Cache[K, V]) shard(k K) *shard[K, V] {
	return &c.shards[maphash.Comparable(c.seed, k)%shards]
}

func (c *Cache[K, V]) Get(k K) (V, bool) {
	s := c.shard(k)
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[k]
	if !ok {
		var zero V
		return zero, false
	}
	if time.Now().After(e.expires) {
		s.remove(e)
		var zero V
		return zero, false
	}
	return e.value, true
}

// Take returns and removes the entry of k.
func (c *Cache[K, V]) Take(k K) (V, bool) {
	s := c.shard(k)
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[k]
	if !ok {
		var zero V
		return zero, false
	}
	s.remove(e)
	if time.Now().After(e.expires) {
		var zero V
		return zero, false
	}
	return e.value, true
}

func (c *Cache[K, V]) Set(k K, v V) {
	c.SetUntil(k, v, time.Now().Add(c.ttl))
}

// SetUntil caches v until the earlier of expires and the cache's ttl.
func (c *Cache[K, V]) SetUntil(k K, v V, expires time.Time) {
	if limit := time.Now().Add(c.ttl); expires.After(limit) {
		expires = limit
	}
	s := c.shard(k)
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[k]; ok {
		e.value, e.expires = v, expires
		s.unlink(e)
		s.push(e)
		return
	}
	if len(s.m) >= c.perShard {
		s.remove(s.head)
	}
	e := &entry[K, V]{key: k, value: v, expires: expires}
	s.m[k] = e
	s.push(e)
}

func (c *Cache[K, V]) Delete(k K) {
	s := c.shard(k)
	s.mu.Lock()
	if e, ok := s.m[k]; ok {
		s.remove(e)
	}
	s.mu.Unlock()
}

// DeleteFunc removes every entry for which drop returns true.
func (c *Cache[K, V]) DeleteFunc(drop func(K, V) bool) {
	for i := range c.shards {
		s := &c.shards[i]
		s.mu.Lock()
		for e := s.head; e != nil; {
			next := e.next
			if drop(e.key, e.value) {
				s.remove(e)
			}
			e = next
		}
		s.mu.Unlock()
	}
}
