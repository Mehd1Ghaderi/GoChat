package drpc

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/services"
	"github.com/ssenerg/gochat/internal/services/directory"
	"github.com/ssenerg/gochat/internal/services/hub"
)

// countingDirectory approves everyone it is asked about, counting the
// checks and the people checked; the first check waits for gate.
type countingDirectory struct {
	checks, people atomic.Int64
	gate           chan struct{}
}

func (d *countingDirectory) Receive(c *actor.Context) {
	if msg, ok := c.Message().(directory.Acquainted); ok {
		if d.checks.Add(1) == 1 {
			<-d.gate
		}
		d.people.Add(int64(len(msg.UserIDs)))
		actors.Reply(c, msg.UserIDs)
	}
}

type nopStream struct{}

func (nopStream) Send(*pb.ConnectResponse) error { return nil }
func (nopStream) Close() error                   { return nil }

func TestAFloodOfWatchesCostsTwoChecks(t *testing.T) {
	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hubPID := engine.Spawn(hub.New(zap.NewNop(), &hub.Config{GatewayQueueLimit: 1 << 16, PresenceGrace: time.Second}), "hub")
	dir := &countingDirectory{gate: make(chan struct{})}
	dirPID := engine.Spawn(func() actor.Receiver { return dir }, "directory")
	t.Cleanup(func() {
		<-engine.Poison(dirPID).Done()
		<-engine.Poison(hubPID).Done()
	})
	if _, err := actors.Ask[hub.Registered](ctx, engine, hubPID, hub.RegisterGateway{ID: "gw", Stream: nopStream{}}); err != nil {
		t.Fatal(err)
	}
	engine.Send(hubPID, hub.Attach{GatewayID: "gw", ConnID: 1, User: &pb.User{Id: "me"}, SessionID: "s", ExpiresAt: time.Now().Add(time.Hour)})

	s := &gatewayService{&base{sys: &services.System{Engine: engine, Hub: hubPID, Directory: dirPID}, watchChecks: make(chan struct{}, maxWatchChecks)}}
	checks := newWatchChecks(func(w *pb.Watch, seq uint64) { s.watch(ctx, "gw", w, seq) })
	idle := func() {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			checks.mu.Lock()
			n := len(checks.waiting)
			checks.mu.Unlock()
			if n == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the checks never finished")
			}
		}
	}

	// 200 frames of 1000 new people each, as fast as they come, while the
	// first check runs.
	var last []string
	for f := range 200 {
		last = make([]string, 1000)
		for i := range last {
			last[i] = fmt.Sprintf("u%d-%d", f, i)
		}
		checks.add(&pb.Watch{ConnId: 1, UserIds: last})
	}
	close(dir.gate)
	idle()
	if got := dir.checks.Load(); got != 2 {
		t.Fatalf("%d checks, want the first and the newest", got)
	}
	if got := dir.people.Load(); got != 2000 {
		t.Fatalf("%d people checked, want 2000", got)
	}
	watched, err := actors.Ask[[]string](ctx, engine, hubPID, hub.WatchedBy{UserID: "me"})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(watched)
	want := slices.Sorted(slices.Values(last))
	if !slices.Equal(watched, want) {
		t.Fatalf("the connection watches %d people, not the newest list", len(watched))
	}

	// The same list again, or a shorter one, needs no check at all.
	checks.add(&pb.Watch{ConnId: 1, UserIds: last})
	idle()
	checks.add(&pb.Watch{ConnId: 1, UserIds: last[:10]})
	idle()
	if got := dir.checks.Load(); got != 2 {
		t.Fatalf("%d checks after repeating the list", got)
	}
	watched, err = actors.Ask[[]string](ctx, engine, hubPID, hub.WatchedBy{UserID: "me"})
	if err != nil {
		t.Fatal(err)
	}
	if len(watched) != 10 {
		t.Fatalf("the connection watches %d people, want 10", len(watched))
	}
}
