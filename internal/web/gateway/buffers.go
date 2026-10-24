package gateway

import (
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"golang.org/x/time/rate"
)

// outbox holds what a socket has yet to write, bounded by bytes: a browser
// that falls further behind loses its socket. Fragments are often shared
// between sockets, so the bound is on what each one holds on to.
type outbox struct {
	mu    sync.Mutex
	items [][]byte
	// size counts the bytes queued or being written.
	size  int
	limit int
	// closed stops the outbox; code, when set, closes the socket after what
	// was queued is written.
	closed bool
	code   websocket.StatusCode
	ready  chan struct{}
}

func newOutbox(limit int) *outbox {
	return &outbox{limit: limit, ready: make(chan struct{}, 1)}
}

func (o *outbox) signal() {
	select {
	case o.ready <- struct{}{}:
	default:
	}
}

// push queues b, reporting false when the outbox is closed or b would take
// it past its limit.
func (o *outbox) push(b []byte) bool {
	o.mu.Lock()
	if o.closed || o.size+len(b) > o.limit {
		o.mu.Unlock()
		return false
	}
	o.items = append(o.items, b)
	o.size += len(b)
	o.mu.Unlock()
	o.signal()
	return true
}

// close stops the outbox. With a code the writer finishes what is queued
// and closes the socket with it; without, it drops what is queued.
func (o *outbox) close(code websocket.StatusCode) {
	o.mu.Lock()
	if !o.closed {
		o.closed, o.code = true, code
		if code == 0 {
			o.items, o.size = nil, 0
		}
	}
	o.mu.Unlock()
	o.signal()
}

// take waits for what is queued. done reports the outbox closed, with the
// code to close the socket with once items are written.
func (o *outbox) take() (items [][]byte, done bool, code websocket.StatusCode) {
	for {
		o.mu.Lock()
		items, o.items = o.items, nil
		done, code = o.closed, o.code
		o.mu.Unlock()
		if len(items) > 0 || done {
			return items, done, code
		}
		<-o.ready
	}
}

// sent releases the bytes of an item written.
func (o *outbox) sent(n int) {
	o.mu.Lock()
	o.size -= n
	o.mu.Unlock()
}

// Inflow admits what a browser sends to its session: at the frame rate, and
// up to a budget of bytes the session has yet to handle.
type Inflow struct {
	limiter *rate.Limiter
	budget  int64
	pending atomic.Int64
}

func NewInflow(cfg *Config) *Inflow {
	return &Inflow{limiter: rate.NewLimiter(rate.Limit(cfg.FrameRate), cfg.FrameBurst), budget: cfg.RecvBuffer}
}

// Admit reports whether a frame of n bytes may go to the session; a browser
// refused sends too fast or too much.
func (in *Inflow) Admit(n int) bool {
	if !in.limiter.Allow() {
		return false
	}
	if in.pending.Add(int64(n)) > in.budget {
		in.pending.Add(-int64(n))
		return false
	}
	return true
}

// done releases a frame the session handled or dropped.
func (in *Inflow) done(n int) {
	in.pending.Add(-int64(n))
}
