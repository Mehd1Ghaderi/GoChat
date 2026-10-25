package gateway

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
)

// fakeStream plays the chat server's end of a gateway stream.
type fakeStream struct {
	pb.DRPCGatewayService_ConnectClient
	sent   chan *pb.ConnectRequest
	recv   chan *pb.ConnectResponse
	closed chan struct{}
	once   sync.Once
}

func newFakeStream() *fakeStream {
	f := &fakeStream{sent: make(chan *pb.ConnectRequest, 1024), recv: make(chan *pb.ConnectResponse, 1024), closed: make(chan struct{})}
	f.recv <- &pb.ConnectResponse{Frame: &pb.ConnectResponse_Welcome{Welcome: &pb.Welcome{}}}
	return f
}

func (f *fakeStream) Send(req *pb.ConnectRequest) error {
	select {
	case <-f.closed:
		return io.EOF
	default:
	}
	f.sent <- req
	return nil
}

func (f *fakeStream) Recv() (*pb.ConnectResponse, error) {
	select {
	case r := <-f.recv:
		return r, nil
	case <-f.closed:
		return nil, io.EOF
	}
}

func (f *fakeStream) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

// next waits for the next frame of type T the router sends.
func next[T any](t *testing.T, f *fakeStream, within time.Duration) T {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case req := <-f.sent:
			if v, ok := any(req.Frame).(T); ok {
				return v
			}
		case <-deadline:
			var zero T
			t.Fatalf("no %T within %v", zero, within)
			return zero
		}
	}
}

func testConfig() *Config {
	return &Config{
		ReconnectMin: 10 * time.Millisecond, ReconnectMax: 20 * time.Millisecond, MaxPerUser: 10,
		AttachRate: 1000, AttachBurst: 1000, ResyncRate: 1000, ResyncBurst: 1000,
	}
}

// startRouter runs a router whose streams are the ones given, in turn.
func startRouter(t *testing.T, cfg *Config, streams ...*fakeStream) (*actor.Engine, *actor.PID) {
	t.Helper()
	var mu sync.Mutex
	open := func(context.Context) (pb.DRPCGatewayService_ConnectClient, func(), error) {
		mu.Lock()
		defer mu.Unlock()
		if len(streams) == 0 {
			return nil, nil, errors.New("no more streams")
		}
		s := streams[0]
		streams = streams[1:]
		return s, func() { _ = s.Close() }, nil
	}
	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		t.Fatal(err)
	}
	pid := engine.Spawn(newRouter(zap.NewNop(), open, cfg), "router")
	t.Cleanup(func() { <-engine.Poison(pid).Done() })
	return engine, pid
}

// inbox is a stand-in session that keeps what the router sends it.
type inbox struct {
	pid *actor.PID
	got chan any
}

func newInbox(engine *actor.Engine, name string) *inbox {
	in := &inbox{got: make(chan any, 256)}
	in.pid = engine.SpawnFunc(func(c *actor.Context) {
		switch c.Message().(type) {
		case actor.Started, actor.Stopped, actor.Initialized:
		default:
			in.got <- c.Message()
		}
	}, name)
	return in
}

func (in *inbox) expect(t *testing.T, within time.Duration) any {
	t.Helper()
	select {
	case m := <-in.got:
		return m
	case <-time.After(within):
		t.Fatal("the session got nothing")
		return nil
	}
}

func attached(id uint64, user string) *pb.ConnectResponse {
	return &pb.ConnectResponse{Frame: &pb.ConnectResponse_Attached{Attached: &pb.Attached{ConnId: id, User: &pb.User{Id: user}, SessionId: "s"}}}
}

func TestAttachesArePaced(t *testing.T) {
	cfg := testConfig()
	cfg.AttachRate, cfg.AttachBurst = 20, 2
	stream := newFakeStream()
	engine, router := startRouter(t, cfg, stream)
	for i := range 10 {
		engine.Send(router, register{connID: uint64(i + 1), token: "t", pid: newInbox(engine, "s").pid})
	}
	start := time.Now()
	var at []time.Duration
	for range 10 {
		next[*pb.ConnectRequest_Attach](t, stream, 2*time.Second)
		at = append(at, time.Since(start))
	}
	// Two at once, then one every 50ms.
	if at[1] > 100*time.Millisecond || at[4] < 100*time.Millisecond || at[9] < 300*time.Millisecond {
		t.Fatalf("attached at %v", at)
	}
}

func TestReconnectReattachesInTurn(t *testing.T) {
	first, second := newFakeStream(), newFakeStream()
	engine, router := startRouter(t, testConfig(), first, second)
	sessions := make([]*inbox, 3)
	for i := range sessions {
		sessions[i] = newInbox(engine, "s")
		engine.Send(router, register{connID: uint64(i + 1), token: "t", pid: sessions[i].pid})
	}
	for range sessions {
		a := next[*pb.ConnectRequest_Attach](t, first, time.Second)
		first.recv <- attached(a.Attach.ConnId, "u")
	}
	for _, s := range sessions {
		if r, ok := s.expect(t, time.Second).(Ready); !ok || r.Again {
			t.Fatalf("first attach: %+v", r)
		}
	}
	_ = first.Close()
	for range sessions {
		a := next[*pb.ConnectRequest_Attach](t, second, time.Second)
		second.recv <- attached(a.Attach.ConnId, "u")
	}
	for _, s := range sessions {
		if r, ok := s.expect(t, time.Second).(Ready); !ok || !r.Again {
			t.Fatalf("attach after a reconnect: %+v", r)
		}
	}
}

func TestResyncsArePacedAndSpread(t *testing.T) {
	limiter := rate.NewLimiter(10, 1)
	var waits []time.Duration
	for range 5 {
		waits = append(waits, resyncWait(limiter, time.Second))
	}
	// Each takes its turn, 100ms apart, then up to a second more.
	for i, w := range waits {
		turn := time.Duration(i) * 100 * time.Millisecond
		if w < turn-10*time.Millisecond || w > turn+time.Second+10*time.Millisecond {
			t.Fatalf("resync %d waits %v", i, w)
		}
	}
}

// attachAll registers a session per user on the stream and attaches it.
func attachAll(t *testing.T, engine *actor.Engine, router *actor.PID, stream *fakeStream, users ...string) []*inbox {
	t.Helper()
	sessions := make([]*inbox, len(users))
	for i, u := range users {
		sessions[i] = newInbox(engine, "s")
		engine.Send(router, register{connID: uint64(i + 1), token: "t", pid: sessions[i].pid})
		a := next[*pb.ConnectRequest_Attach](t, stream, time.Second)
		stream.recv <- attached(a.Attach.ConnId, u)
		if _, ok := sessions[i].expect(t, time.Second).(Ready); !ok {
			t.Fatal("not ready")
		}
	}
	return sessions
}

func (in *inbox) none(t *testing.T) {
	t.Helper()
	select {
	case m := <-in.got:
		t.Fatalf("the session got %T", m)
	case <-time.After(100 * time.Millisecond):
	}
}

func event(ev *pb.Event) *pb.ConnectResponse {
	return &pb.ConnectResponse{Frame: &pb.ConnectResponse_Event{Event: ev}}
}

func TestEventsGoToTheirConnections(t *testing.T) {
	stream := newFakeStream()
	engine, router := startRouter(t, testConfig(), stream)
	s := attachAll(t, engine, router, stream, "a", "a", "b")
	stream.recv <- event(&pb.Event{ConnIds: []uint64{2, 3, 99}, Payload: &pb.Event_Typing{Typing: &pb.Typing{}}})
	s[1].expect(t, time.Second)
	s[2].expect(t, time.Second)
	s[0].none(t)
}

func TestWatchesReachTheServer(t *testing.T) {
	first, second := newFakeStream(), newFakeStream()
	engine, router := startRouter(t, testConfig(), first, second)
	attachAll(t, engine, router, first, "a")
	engine.Send(router, watching{connID: 1, users: []string{"b", "c"}})
	if w := next[*pb.ConnectRequest_Watch](t, first, time.Second); w.Watch.ConnId != 1 || !slices.Equal(w.Watch.UserIds, []string{"b", "c"}) {
		t.Fatalf("watch %+v", w.Watch)
	}
	// After a reconnect the watch follows the attach.
	_ = first.Close()
	a := next[*pb.ConnectRequest_Attach](t, second, time.Second)
	second.recv <- attached(a.Attach.ConnId, "a")
	if w := next[*pb.ConnectRequest_Watch](t, second, time.Second); !slices.Equal(w.Watch.UserIds, []string{"b", "c"}) {
		t.Fatalf("watch after a reconnect %+v", w.Watch)
	}
}

func TestViewsReachTheServer(t *testing.T) {
	first, second := newFakeStream(), newFakeStream()
	engine, router := startRouter(t, testConfig(), first, second)
	attachAll(t, engine, router, first, "a")
	engine.Send(router, viewing{connID: 1, channelID: "ch"})
	if v := next[*pb.ConnectRequest_View](t, first, time.Second); v.View.ConnId != 1 || v.View.ChannelId != "ch" {
		t.Fatalf("view %+v", v.View)
	}
	// After a reconnect the view follows the attach.
	_ = first.Close()
	a := next[*pb.ConnectRequest_Attach](t, second, time.Second)
	second.recv <- attached(a.Attach.ConnId, "a")
	if v := next[*pb.ConnectRequest_View](t, second, time.Second); v.View.ChannelId != "ch" {
		t.Fatalf("view after a reconnect %+v", v.View)
	}
}
