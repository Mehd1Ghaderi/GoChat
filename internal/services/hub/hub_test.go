package hub

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/actors"
)

func TestStatus(t *testing.T) {
	h := &hub{online: map[string]map[string]int{"a": {"gw": 1}}, chosen: map[string]choice{}}
	cases := []struct {
		chosen choice
		want   pb.Status
	}{
		{choice{}, pb.Status_STATUS_ONLINE},
		{choice{status: pb.Status_STATUS_AWAY}, pb.Status_STATUS_AWAY},
		{choice{status: pb.Status_STATUS_DND}, pb.Status_STATUS_DND},
		{choice{status: pb.Status_STATUS_DND, dndUntil: time.Now().Add(time.Hour)}, pb.Status_STATUS_DND},
		{choice{status: pb.Status_STATUS_DND, dndUntil: time.Now().Add(-time.Second)}, pb.Status_STATUS_ONLINE},
		{choice{status: pb.Status_STATUS_OFFLINE}, pb.Status_STATUS_OFFLINE},
	}
	for _, c := range cases {
		h.chosen["a"] = c.chosen
		if got := h.status("a"); got != c.want {
			t.Errorf("%+v: got %v, want %v", c.chosen, got, c.want)
		}
	}
	if got := h.status("b"); got != pb.Status_STATUS_OFFLINE {
		t.Errorf("a user with no connection is %v", got)
	}
}

// wire is a gateway's stream, as the hub writes it.
type wire struct{ got chan *pb.ConnectResponse }

func (w *wire) Send(r *pb.ConnectResponse) error {
	w.got <- r
	return nil
}

func (w *wire) Close() error { return nil }

// event waits for the next event addressed to connections, skipping the
// rest.
func (w *wire) event(t *testing.T) *pb.Event {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case r := <-w.got:
			if ev := r.GetEvent(); ev != nil && !ev.Broadcast {
				return ev
			}
		case <-deadline:
			t.Fatal("no event")
			return nil
		}
	}
}

func (w *wire) none(t *testing.T) {
	t.Helper()
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case r := <-w.got:
			if ev := r.GetEvent(); ev != nil && !ev.Broadcast {
				t.Fatalf("unexpected event for %v", ev.ConnIds)
			}
		case <-deadline:
			return
		}
	}
}

type testHub struct {
	t      *testing.T
	engine *actor.Engine
	pid    *actor.PID
	wires  map[string]*wire
}

func startHub(t *testing.T, gateways ...string) *testHub {
	t.Helper()
	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		t.Fatal(err)
	}
	h := &testHub{t: t, engine: engine, wires: map[string]*wire{}}
	h.pid = engine.Spawn(New(zap.NewNop(), &Config{GatewayQueueLimit: 1 << 16, PresenceGrace: time.Second}), "hub")
	t.Cleanup(func() { <-engine.Poison(h.pid).Done() })
	for _, id := range gateways {
		w := &wire{got: make(chan *pb.ConnectResponse, 1024)}
		if _, err := actors.Ask[Registered](context.Background(), engine, h.pid, RegisterGateway{ID: id, Stream: w}); err != nil {
			t.Fatal(err)
		}
		h.wires[id] = w
	}
	return h
}

func (h *testHub) attach(gw string, conn uint64, user string) {
	h.engine.Send(h.pid, Attach{GatewayID: gw, ConnID: conn, User: &pb.User{Id: user}, SessionID: "s" + user, ExpiresAt: time.Now().Add(time.Hour)})
}

func sorted(ids []uint64) []uint64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

func typing(ch string) *pb.Event {
	return &pb.Event{Payload: &pb.Event_Typing{Typing: &pb.Typing{ChannelId: ch}}}
}

func TestDeliverToConnections(t *testing.T) {
	h := startHub(t, "gw1", "gw2")
	h.attach("gw1", 1, "a")
	h.attach("gw1", 2, "a")
	h.attach("gw1", 3, "c")
	h.attach("gw2", 7, "b")
	members := Members{"a": {}, "b": {}, "x": {}}
	h.engine.Send(h.pid, Deliver{Members: members, Event: typing("ch")})
	if got := sorted(h.wires["gw1"].event(t).ConnIds); !slices.Equal(got, []uint64{1, 2}) {
		t.Fatalf("gw1 got %v", got)
	}
	if got := h.wires["gw2"].event(t).ConnIds; !slices.Equal(got, []uint64{7}) {
		t.Fatalf("gw2 got %v", got)
	}
	h.engine.Send(h.pid, Deliver{UserIDs: []string{"c"}, Event: typing("ch")})
	if got := h.wires["gw1"].event(t).ConnIds; !slices.Equal(got, []uint64{3}) {
		t.Fatalf("one user: %v", got)
	}
	online, err := actors.Ask[[]string](context.Background(), h.engine, h.pid, Online{Members: members})
	if slices.Sort(online); err != nil || !slices.Equal(online, []string{"a", "b"}) {
		t.Fatalf("online: %v %v", online, err)
	}
}

func TestTypingGoesToViewers(t *testing.T) {
	h := startHub(t, "gw1", "gw2")
	h.attach("gw1", 1, "a")
	h.attach("gw1", 2, "a")
	h.attach("gw1", 3, "c")
	h.attach("gw2", 7, "b")
	h.engine.Send(h.pid, View{GatewayID: "gw1", ConnID: 1, ChannelID: "ch"})
	h.engine.Send(h.pid, View{GatewayID: "gw1", ConnID: 2, ChannelID: "other"})
	// c shows the channel without being a member.
	h.engine.Send(h.pid, View{GatewayID: "gw1", ConnID: 3, ChannelID: "ch"})
	h.engine.Send(h.pid, View{GatewayID: "gw2", ConnID: 7, ChannelID: "ch"})
	members := Members{"a": {}, "b": {}}
	h.engine.Send(h.pid, Deliver{Members: members, Viewing: "ch", Except: "b", Event: typing("ch")})
	if got := h.wires["gw1"].event(t).ConnIds; !slices.Equal(got, []uint64{1}) {
		t.Fatalf("gw1 got %v", got)
	}
	h.wires["gw2"].none(t)
	// Leaving the channel, or the socket, stops it.
	h.engine.Send(h.pid, View{GatewayID: "gw1", ConnID: 1, ChannelID: ""})
	h.engine.Send(h.pid, View{GatewayID: "gw1", ConnID: 2, ChannelID: "ch"})
	h.engine.Send(h.pid, Detach{GatewayID: "gw1", ConnID: 2})
	h.engine.Send(h.pid, Deliver{Members: members, Viewing: "ch", Event: typing("ch")})
	if got := h.wires["gw2"].event(t).ConnIds; !slices.Equal(got, []uint64{7}) {
		t.Fatalf("gw2 got %v", got)
	}
	h.wires["gw1"].none(t)
}

// busyHub has two gateways, 10,000 members of a channel of whom 2,000 are
// online, and 5 of them showing it.
func busyHub() (*hub, Members) {
	h := &hub{
		gateways: map[string]*gateway{}, online: map[string]map[string]int{},
		conns: map[string][]connRef{}, viewers: map[string]map[*gateway]map[uint64]struct{}{},
	}
	for _, id := range []string{"gw1", "gw2"} {
		h.gateways[id] = &gateway{id: id, pending: new(atomic.Int64), conns: map[uint64]*conn{}}
	}
	members := Members{}
	for i := range 10_000 {
		user := fmt.Sprintf("%08d-0000-0000-0000-000000000000", i)
		members[user] = struct{}{}
		if i%5 != 0 {
			continue
		}
		gw := h.gateways[[]string{"gw1", "gw2"}[i%2]]
		id := uint64(i + 1)
		gw.conns[id] = &conn{userID: user}
		h.conns[user] = []connRef{{gw: gw, id: id}}
		h.online[user] = map[string]int{gw.id: 1}
		if i < 25 {
			h.view(gw, id, gw.conns[id], "ch")
		}
	}
	return h, members
}

// BenchmarkRoute routes one event of a busy channel: by user id to every
// online member, as events went, then by connection to the online members,
// as messages go, and to those showing the channel, as typing goes.
func BenchmarkRoute(b *testing.B) {
	h, members := busyHub()
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	b.Run("user-ids", func(b *testing.B) {
		var wire int
		for range b.N {
			wire = 0
			byGateway := map[string][]string{}
			for _, uid := range ids {
				for gw := range h.online[uid] {
					byGateway[gw] = append(byGateway[gw], uid)
				}
			}
			for _, uids := range byGateway {
				for _, uid := range uids {
					wire += 2 + len(uid)
				}
			}
		}
		b.ReportMetric(float64(wire), "wire-bytes/op")
	})
	for _, c := range []struct {
		name string
		msg  Deliver
	}{
		{"message", Deliver{Members: members, Event: typing("ch")}},
		{"typing", Deliver{Members: members, Viewing: "ch", Event: typing("ch")}},
	} {
		b.Run(c.name, func(b *testing.B) {
			var wire int
			for range b.N {
				wire = 0
				for _, conns := range h.route(c.msg) {
					wire += proto.Size(&pb.Event{ConnIds: conns})
				}
			}
			b.ReportMetric(float64(wire), "wire-bytes/op")
		})
	}
}

type fakeStream struct{ sent chan *pb.ConnectResponse }

func (s *fakeStream) Send(r *pb.ConnectResponse) error {
	s.sent <- r
	return nil
}

func (s *fakeStream) Close() error { return nil }

// frame waits for the first frame f accepts.
func (s *fakeStream) frame(t *testing.T, accept func(*pb.ConnectResponse) bool) *pb.ConnectResponse {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case r := <-s.sent:
			if accept(r) {
				return r
			}
		case <-deadline:
			t.Fatal("no such frame")
			return nil
		}
	}
}

func TestRevokeAllClosesEveryConnection(t *testing.T) {
	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		t.Fatal(err)
	}
	pid := engine.Spawn(New(zap.NewNop(), &Config{GatewayQueueLimit: 64}), "hub")
	defer func() { <-engine.Poison(pid).Done() }()
	stream := &fakeStream{sent: make(chan *pb.ConnectResponse, 64)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := actors.Ask[Registered](ctx, engine, pid, RegisterGateway{ID: "gw", Stream: stream}); err != nil {
		t.Fatal(err)
	}
	attach := func(conn uint64, user, session string) {
		engine.Send(pid, Attach{GatewayID: "gw", ConnID: conn, User: &pb.User{Id: user}, SessionID: session, ExpiresAt: time.Now().Add(time.Hour)})
		stream.frame(t, func(r *pb.ConnectResponse) bool { return r.GetAttached().GetConnId() == conn })
	}
	attach(1, "u", "s1")
	attach(2, "u", "s2")
	attach(3, "u", "s2")
	attach(4, "v", "s3")

	engine.Send(pid, RevokeSessions{UserID: "u", All: true})
	got := stream.frame(t, func(r *pb.ConnectResponse) bool { return r.GetSessionsRevoked() != nil })
	if ids := got.GetSessionsRevoked().SessionIds; !slices.Equal(ids, []string{"s1", "s2"}) {
		t.Fatalf("revoked %v", ids)
	}
	engine.Send(pid, Attach{GatewayID: "gw", ConnID: 5, User: &pb.User{Id: "u"}, SessionID: "s1", ExpiresAt: time.Now().Add(time.Hour)})
	stream.frame(t, func(r *pb.ConnectResponse) bool { return r.GetAttachRejected().GetConnId() == 5 })
	online, err := actors.Ask[[]string](ctx, engine, pid, Online{Members: Members{"u": {}, "v": {}}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(online, []string{"v"}) {
		t.Fatalf("online after the revocation: %v", online)
	}
}
