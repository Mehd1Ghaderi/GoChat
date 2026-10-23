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
	for i := range 10000 {
		c.Set(string(rune(i)), i)
	}
	n := 0
	c.DeleteFunc(func(string, int) bool { n++; return false })
	if n > 64+shards {
		t.Fatalf("cache grew to %d entries", n)
	}
}
