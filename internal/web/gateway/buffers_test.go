package gateway

import (
	"bytes"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/coder/websocket"
)

func TestOutboxBoundsBytes(t *testing.T) {
	o := newOutbox(10)
	if !o.push([]byte("12345")) || !o.push([]byte("6789")) {
		t.Fatal("refused what fits")
	}
	if o.push([]byte("ab")) {
		t.Fatal("took more than its limit")
	}
	items, done, _ := o.take()
	if done || len(items) != 2 || !bytes.Equal(items[1], []byte("6789")) {
		t.Fatalf("took %q, done %v", items, done)
	}
	// Bytes count until written.
	if o.push([]byte("ab")) {
		t.Fatal("took more while writing")
	}
	o.sent(5)
	o.sent(4)
	if !o.push([]byte("ab")) {
		t.Fatal("refused once written")
	}
}

func TestOutboxClose(t *testing.T) {
	o := newOutbox(100)
	o.push([]byte("last words"))
	o.close(websocket.StatusPolicyViolation)
	o.close(websocket.StatusNormalClosure)
	if o.push([]byte("more")) {
		t.Fatal("took after closing")
	}
	items, done, code := o.take()
	if len(items) != 1 || !done || code != websocket.StatusPolicyViolation {
		t.Fatalf("took %q, done %v, code %v", items, done, code)
	}
	// Closing without a code drops what is queued.
	o = newOutbox(100)
	o.push([]byte("dropped"))
	o.close(0)
	if items, done, _ := o.take(); len(items) != 0 || !done {
		t.Fatalf("took %q after an abort", items)
	}
}

func TestOutboxWakesTheWriter(t *testing.T) {
	o := newOutbox(100)
	got := make(chan []byte)
	go func() {
		items, _, _ := o.take()
		got <- items[0]
	}()
	time.Sleep(10 * time.Millisecond)
	o.push([]byte("hello"))
	select {
	case b := <-got:
		if string(b) != "hello" {
			t.Fatalf("took %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("the writer slept through a push")
	}
}

func TestInflowBudget(t *testing.T) {
	in := NewInflow(&Config{FrameRate: 1000, FrameBurst: 1000, RecvBuffer: 100})
	if !in.Admit(60) || in.Admit(60) {
		t.Fatal("budget not kept")
	}
	in.done(60)
	if !in.Admit(60) {
		t.Fatal("budget not released")
	}
	slow := NewInflow(&Config{FrameRate: 1, FrameBurst: 2, RecvBuffer: 100})
	admitted := 0
	for range 3 {
		if slow.Admit(1) {
			admitted++
		}
	}
	if admitted != 2 {
		t.Fatalf("admitted %d of a burst of 2", admitted)
	}
}

var sink []any

// BenchmarkSocketBuffers measures what a socket's queues cost before it
// sends anything: counted queues as they were, against byte-bounded ones.
func BenchmarkSocketBuffers(b *testing.B) {
	// The size of what the channel held: a fragment, a close code and flag.
	type outMsg struct {
		_ []byte
		_ websocket.StatusCode
		_ bool
	}
	b.Run("counted", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			sink = []any{make(chan outMsg, 512), actor.NewInbox(1024)}
		}
	})
	b.Run("bytes", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			sink = []any{newOutbox(1 << 20), actor.NewInbox(32)}
		}
	})
}
