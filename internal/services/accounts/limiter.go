package accounts

import (
	"sync"
	"time"
)

const limiterCapacity = 100_000

// limiter counts failures per key in a fixed window.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	m      map[string]*window
}

type window struct {
	start time.Time
	count int
}

func newLimiter(max int, w time.Duration) *limiter {
	return &limiter{max: max, window: w, m: make(map[string]*window)}
}

func (l *limiter) blocked(key string) bool {
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

func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w := l.m[key]
	if w == nil || now.Sub(w.start) > l.window {
		if len(l.m) >= limiterCapacity {
			l.sweep(now)
		}
		w = &window{start: now}
		l.m[key] = w
	}
	w.count++
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.m, key)
	l.mu.Unlock()
}

func (l *limiter) sweep(now time.Time) {
	for k, w := range l.m {
		if now.Sub(w.start) > l.window {
			delete(l.m, k)
		}
	}
	// Under a flood of distinct keys, forget the oldest half rather than grow.
	if len(l.m) >= limiterCapacity {
		n := 0
		for k := range l.m {
			if n >= limiterCapacity/2 {
				break
			}
			delete(l.m, k)
			n++
		}
	}
}
