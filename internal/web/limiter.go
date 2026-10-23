package web

import (
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const limiterMaxKeys = 200_000

// ipLimiter keeps one token bucket per client address.
type ipLimiter struct {
	mu    sync.Mutex
	rate  rate.Limit
	burst int
	m     map[string]*bucket
	swept time.Time
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

func newIPLimiter(r rate.Limit, burst int) *ipLimiter {
	return &ipLimiter{rate: r, burst: burst, m: make(map[string]*bucket), swept: time.Now()}
}

// Allow takes a token from the bucket of ip, or reports how long until one
// is free.
func (l *ipLimiter) Allow(ip string) (bool, time.Duration) {
	key := addressKey(ip)
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.swept) > time.Minute || len(l.m) >= limiterMaxKeys {
		l.sweep(now)
	}
	b := l.m[key]
	if b == nil {
		b = &bucket{lim: rate.NewLimiter(l.rate, l.burst)}
		l.m[key] = b
	}
	b.seen = now
	res := b.lim.ReserveN(now, 1)
	if d := res.DelayFrom(now); d > 0 {
		res.CancelAt(now)
		return false, d
	}
	return true, 0
}

// addressKey groups IPv6 addresses by their /64, which one host can rotate
// through.
func addressKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is6() || addr.Is4In6() {
		return ip
	}
	p, _ := addr.Prefix(64)
	return p.String()
}

// sweep forgets idle buckets: a full one behaves like a fresh one.
func (l *ipLimiter) sweep(now time.Time) {
	l.swept = now
	idle := time.Duration(float64(l.burst)/float64(l.rate)*float64(time.Second)) + time.Minute
	for k, b := range l.m {
		if now.Sub(b.seen) > idle {
			delete(l.m, k)
		}
	}
	if len(l.m) >= limiterMaxKeys {
		n := 0
		for k := range l.m {
			if n >= limiterMaxKeys/2 {
				break
			}
			delete(l.m, k)
			n++
		}
	}
}
