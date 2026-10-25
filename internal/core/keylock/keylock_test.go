package keylock

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSerializesAKey(t *testing.T) {
	l := New()
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		holders int
		most    int
	)
	for range 50 {
		wg.Go(func() {
			unlock, err := l.Lock(context.Background(), "team")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			holders++
			most = max(most, holders)
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			holders--
			mu.Unlock()
			unlock()
		})
	}
	wg.Wait()
	if most != 1 {
		t.Fatalf("%d held the key at once", most)
	}
}

func TestGivesUpWithItsContext(t *testing.T) {
	l := New()
	unlock, _ := l.Lock(context.Background(), "k")
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := l.Lock(ctx, "k"); err == nil {
		t.Fatal("locked a held key")
	}
}
