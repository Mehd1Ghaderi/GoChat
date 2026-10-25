package drpc

import (
	"context"
	"sync"
	"testing"
	"time"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
)

func TestWatchChecksTakeTheLatestFewAtATime(t *testing.T) {
	checks := newWatchChecks(2, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	var (
		mu            sync.Mutex
		running, most int
		lists         = map[uint64][]int{}
		seqs          = map[uint64][]uint64{}
	)
	check := func(_ context.Context, w *pb.Watch, seq uint64) {
		mu.Lock()
		running++
		most = max(most, running)
		lists[w.ConnId] = append(lists[w.ConnId], len(w.UserIds))
		seqs[w.ConnId] = append(seqs[w.ConnId], seq)
		mu.Unlock()
		<-release
		mu.Lock()
		running--
		mu.Unlock()
	}
	// One connection sends 50 lists while its check waits, and five others
	// one each.
	for n := 1; n <= 50; n++ {
		checks.add(ctx, &pb.Watch{ConnId: 1, UserIds: make([]string, n)}, check)
	}
	for c := uint64(2); c <= 6; c++ {
		checks.add(ctx, &pb.Watch{ConnId: c, UserIds: make([]string, 1)}, check)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		checks.mu.Lock()
		idle := len(checks.running) == 0
		checks.mu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the checks did not finish")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if most > 2 {
		t.Fatalf("%d checks ran at once", most)
	}
	if got := lists[1]; len(got) == 0 || len(got) > 2 || got[len(got)-1] != 50 {
		t.Fatalf("the busy connection's checks: %v", got)
	}
	if s := seqs[1]; len(s) == 2 && s[1] <= s[0] {
		t.Fatalf("a connection's checks out of order: %v", s)
	}
	for c := uint64(2); c <= 6; c++ {
		if len(lists[c]) != 1 {
			t.Fatalf("connection %d checked %d times", c, len(lists[c]))
		}
	}
}
