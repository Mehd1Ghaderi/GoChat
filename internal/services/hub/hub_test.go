package hub

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"

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
	online, err := actors.Ask[[]string](ctx, engine, pid, Online{UserIDs: []string{"u", "v"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(online, []string{"v"}) {
		t.Fatalf("online after the revocation: %v", online)
	}
}
