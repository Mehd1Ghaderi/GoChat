// Package window counts events per key in fixed time windows.
package window

import (
	"sync"
	"time"
)

const capacity = 100_000

// Counter counts events per key in a fixed window, forgetting the oldest keys
// rather than growing past a fixed size.
type Counter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	m      map[string]*entry
}

type entry struct {
	start time.Time
	count int
}

func New(limit int, w time.Duration) *Counter {
	return &Counter{max: limit, window: w, m: make(map[string]*entry)}
}

// Blocked reports whether key reached the limit in its current window.
func (l *Counter) Blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.m[key]
	if w == nil {
		return false
	}
	if time.Since(w.start) > l.window {
		delete(l.m, key)
		return false
	}
	return w.count >= l.max
}

// Add counts one event for key.
func (l *Counter) Add(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w := l.m[key]
	if w == nil || now.Sub(w.start) > l.window {
		if len(l.m) >= capacity {
			l.sweep(now)
		}
		w = &entry{start: now}
		l.m[key] = w
	}
	w.count++
}

func (l *Counter) Reset(key string) {
	l.mu.Lock()
	delete(l.m, key)
	l.mu.Unlock()
}

func (l *Counter) sweep(now time.Time) {
	for k, w := range l.m {
		if now.Sub(w.start) > l.window {
			delete(l.m, k)
		}
	}
	// Under a flood of distinct keys, forget the oldest half rather than grow.
	if len(l.m) >= capacity {
		n := 0
		for k := range l.m {
			if n >= capacity/2 {
				break
			}
			delete(l.m, k)
			n++
		}
	}
}
