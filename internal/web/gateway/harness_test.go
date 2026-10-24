package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/coder/websocket"
	"go.uber.org/zap"
	"storj.io/drpc/drpcmux"
	"storj.io/drpc/drpcserver"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
	"github.com/ssenerg/gochat/internal/web/render"
)

// chatServer answers the calls a session makes, counting the sidebar loads.
type chatServer struct {
	pb.DRPCChannelServiceUnimplementedServer
	sidebars atomic.Int32
	channels []*pb.Channel
}

func (s *chatServer) ListChannels(context.Context, *pb.ListChannelsRequest) (*pb.ListChannelsResponse, error) {
	s.sidebars.Add(1)
	return &pb.ListChannelsResponse{Channels: s.channels}, nil
}

func (s *chatServer) ListMutedChannels(context.Context, *pb.ListMutedChannelsRequest) (*pb.ListMutedChannelsResponse, error) {
	return &pb.ListMutedChannelsResponse{}, nil
}

func (s *chatServer) GetChannel(_ context.Context, req *pb.GetChannelRequest) (*pb.GetChannelResponse, error) {
	return &pb.GetChannelResponse{Channel: &pb.Channel{Id: req.ChannelId}}, nil
}

func serveChat(t *testing.T, srv *chatServer) *drpc.Client {
	t.Helper()
	mux := drpcmux.New()
	if err := pb.DRPCRegisterChannelService(mux, srv); err != nil {
		t.Fatal(err)
	}
	if err := pb.DRPCRegisterMessageService(mux, &pb.DRPCMessageServiceUnimplementedServer{}); err != nil {
		t.Fatal(err)
	}
	if err := pb.DRPCRegisterUserService(mux, &pb.DRPCUserServiceUnimplementedServer{}); err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = drpcserver.New(mux).Serve(ctx, lis) }()
	client, err := drpc.New(&drpc.Config{
		Address: lis.Addr().String(), TLS: &drpc.TLSConfig{}, DialTimeout: time.Second, WriteTimeout: time.Second,
		RequestTimeout: time.Second, PoolCapacity: 4, PoolExpiration: time.Minute, MaxMessageSize: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); cancel() })
	return client
}

// harness runs one session behind a real socket, with a stand-in router.
type harness struct {
	t       *testing.T
	engine  *actor.Engine
	router  *inbox
	session *actor.PID
	browser *websocket.Conn
}

func newHarness(t *testing.T, deps *Deps) *harness {
	t.Helper()
	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, engine: engine, router: newInbox(engine, "router")}
	deps.Router = h.router.pid
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		in := NewInflow(deps.Cfg)
		pid := engine.Spawn(NewSession(deps, 1, drpc.Call{Session: "token"}, conn, in), "session")
		defer engine.Poison(pid)
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil || !in.Admit(len(data)) {
				return
			}
			engine.Send(pid, Frame{Data: data})
		}
	}))
	t.Cleanup(ws.Close)
	h.browser, _, err = websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(ws.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.browser.CloseNow() })
	reg, ok := h.router.expect(t, time.Second).(register)
	if !ok {
		t.Fatal("the session did not register")
	}
	h.session = reg.pid
	return h
}

func (h *harness) send(v any) {
	b, _ := json.Marshal(v)
	if err := h.browser.Write(context.Background(), websocket.MessageText, b); err != nil {
		h.t.Fatal(err)
	}
}

// routed waits for the next message of type T the session sends its router.
func routed[T any](t *testing.T, h *harness) T {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case m := <-h.router.got:
			if v, ok := m.(T); ok {
				return v
			}
		case <-deadline:
			var zero T
			t.Fatalf("the router got no %T", zero)
			return zero
		}
	}
}

func harnessDeps(t *testing.T, srv *chatServer) *Deps {
	t.Helper()
	r, err := render.New(100)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.SendBuffer, cfg.RecvBuffer, cfg.ReadLimit = 1<<20, 1<<20, 1<<16
	cfg.FrameRate, cfg.FrameBurst = 100, 100
	cfg.WriteTimeout, cfg.PingInterval = time.Second, time.Hour
	return &Deps{Logger: zap.NewNop(), Cfg: cfg, Client: serveChat(t, srv), Render: r, Sidebars: NewSidebars()}
}

const (
	teamID = "11111111-1111-1111-1111-111111111111"
	chanID = "22222222-2222-2222-2222-222222222222"
	dmID   = "33333333-3333-3333-3333-333333333333"
)

func sidebarLists() *render.Lists {
	return &render.Lists{
		Team:     &pb.Team{Id: teamID, Name: "acme"},
		Channels: []*pb.Channel{{Id: chanID, Name: "town", DisplayName: "Town"}},
		DMs:      []*pb.Channel{{Id: dmID, Type: pb.ChannelType_CHANNEL_TYPE_DIRECT, Peer: &pb.User{Id: "peer"}}},
	}
}

func TestFirstViewTakesThePagesSidebar(t *testing.T) {
	srv := &chatServer{channels: sidebarLists().DMs}
	deps := harnessDeps(t, srv)
	key := deps.Sidebars.Put(NewSidebar("me", sidebarLists()))
	h := newHarness(t, deps)
	h.engine.Send(h.session, Ready{User: &pb.User{Id: "me"}, SessionID: "s"})
	h.send(map[string]string{"op": "view", "team_id": teamID, "team_name": "acme", "channel_id": chanID, "sidebar": key})
	if v := routed[viewing](t, h); v.channelID != chanID {
		t.Fatalf("viewing %q", v.channelID)
	}
	if w := routed[watching](t, h); len(w.users) != 1 || w.users[0] != "peer" {
		t.Fatalf("watching %v", w.users)
	}
	time.Sleep(100 * time.Millisecond)
	if n := srv.sidebars.Load(); n != 0 {
		t.Fatalf("the sidebar loaded %d times", n)
	}
}

func TestSidebarLoadsWithoutTheKey(t *testing.T) {
	srv := &chatServer{channels: sidebarLists().DMs}
	deps := harnessDeps(t, srv)
	// Another user's sidebar is no use.
	key := deps.Sidebars.Put(NewSidebar("someone else", sidebarLists()))
	h := newHarness(t, deps)
	h.engine.Send(h.session, Ready{User: &pb.User{Id: "me"}, SessionID: "s"})
	h.send(map[string]string{"op": "view", "team_id": teamID, "team_name": "acme", "channel_id": chanID, "sidebar": key})
	if w := routed[watching](t, h); len(w.users) != 1 || w.users[0] != "peer" {
		t.Fatalf("watching %v", w.users)
	}
	if n := srv.sidebars.Load(); n != 1 {
		t.Fatalf("the sidebar loaded %d times", n)
	}
}
