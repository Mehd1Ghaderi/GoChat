package cache

import (
	"testing"
	"time"
)

func TestCache(t *testing.T) {
	c := New[string, int](64, time.Hour)
	c.Set("a", 1)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatal("get")
	}
	c.SetUntil("b", 2, time.Now().Add(-time.Second))
	if _, ok := c.Get("b"); ok {
		t.Fatal("expired entry returned")
	}
	c.Delete("a")
	if _, ok := c.Get("a"); ok {
		t.Fatal("deleted entry returned")
	}
	c.Set("t", 3)
	if v, ok := c.Take("t"); !ok || v != 3 {
		t.Fatal("take")
	}
	if _, ok := c.Take("t"); ok {
		t.Fatal("taken twice")
	}
	for i := range 10000 {
		c.Set(string(rune(i)), i)
	}
	n := 0
	c.DeleteFunc(func(string, int) bool { n++; return false })
	if n > 64+shards {
		t.Fatalf("cache grew to %d entries", n)
	}
}

func TestEvictsOldest(t *testing.T) {
	c := New[int, int](3*shards, time.Hour)
	// Three keys of one shard, which holds three entries.
	s := c.shard(0)
	keys := []int{0}
	for k := 1; len(keys) < 4; k++ {
		if c.shard(k) == s {
			keys = append(keys, k)
		}
	}
	for _, k := range keys[:3] {
		c.Set(k, k)
	}
	// Setting the first again makes the second the oldest, which a fourth
	// key evicts.
	c.Set(keys[0], -1)
	c.Set(keys[3], 3)
	if _, ok := c.Get(keys[1]); ok {
		t.Fatal("the oldest entry stayed")
	}
	for _, k := range []int{keys[0], keys[2], keys[3]} {
		if _, ok := c.Get(k); !ok {
			t.Fatalf("lost %d", k)
		}
	}
	c.DeleteFunc(func(k, _ int) bool { return k == keys[2] })
	if s.head.key != keys[0] || s.tail.key != keys[3] || s.head.next != s.tail || len(s.m) != 2 {
		t.Fatal("list after DeleteFunc")
	}
}

// BenchmarkSetFull writes new keys into a full cache, each evicting another.
func BenchmarkSetFull(b *testing.B) {
	c := New[int, int](100_000, time.Hour)
	for i := range 100_000 {
		c.Set(i, i)
	}
	b.ResetTimer()
	for i := range b.N {
		c.Set(100_000+i, i)
	}
}
