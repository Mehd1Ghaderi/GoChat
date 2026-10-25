package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/coder/websocket"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/proto"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
)

// appHarness runs one app's event socket behind a real websocket, with a
// stand-in router.
type appHarness struct {
	t       *testing.T
	engine  *actor.Engine
	router  *inbox
	session *actor.PID
	app     *websocket.Conn
	// frames and end carry what the app reads: a cancelled read would close
	// the socket, so one reader runs throughout.
	frames chan *pb.ClientFrame
	end    chan error
}

func appDeps(sendBuffer int) *Deps {
	cfg := testConfig()
	cfg.SendBuffer, cfg.RecvBuffer, cfg.ReadLimit = sendBuffer, 1<<20, 1<<16
	cfg.FrameRate, cfg.FrameBurst = 100, 100
	cfg.WriteTimeout, cfg.PingInterval = time.Second, time.Hour
	return &Deps{Logger: zap.NewNop(), Cfg: cfg, Resyncs: rate.NewLimiter(rate.Inf, 1)}
}

func newAppHarness(t *testing.T, deps *Deps) *appHarness {
	t.Helper()
	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		t.Fatal(err)
	}
	h := &appHarness{t: t, engine: engine, router: newInbox(engine, "router")}
	deps.Router = h.router.pid
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		// As the API's handler reads: a watch of the most people fits.
		conn.SetReadLimit(64 << 10)
		in := NewInflow(deps.Cfg)
		pid := engine.Spawn(NewClientSession(deps, 1, drpc.Call{Session: "token"}, conn, in), "client")
		defer engine.Poison(pid)
		for {
			typ, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary && in.Admit(len(data)) {
				engine.Send(pid, Frame{Data: data})
			}
		}
	}))
	t.Cleanup(srv.Close)
	h.app, _, err = websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	h.app.SetReadLimit(1 << 20)
	t.Cleanup(func() { _ = h.app.CloseNow() })
	h.frames, h.end = make(chan *pb.ClientFrame, 64), make(chan error, 1)
	go func() {
		for {
			typ, data, err := h.app.Read(context.Background())
			if err != nil {
				h.end <- err
				return
			}
			f := &pb.ClientFrame{}
			if typ != websocket.MessageBinary || proto.Unmarshal(data, f) != nil {
				t.Errorf("not a ClientFrame: %q", data)
				continue
			}
			h.frames <- f
		}
	}()
	reg, ok := h.router.expect(t, time.Second).(register)
	if !ok || reg.token != "token" {
		t.Fatalf("the session did not register: %+v", reg)
	}
	h.session = reg.pid
	return h
}

// frame waits up to d for the next ClientFrame the app gets; nil when none
// came.
func (h *appHarness) frame(d time.Duration) *pb.ClientFrame {
	select {
	case f := <-h.frames:
		return f
	case <-time.After(d):
		return nil
	}
}

func (h *appHarness) must() *pb.ClientFrame {
	h.t.Helper()
	f := h.frame(2 * time.Second)
	if f == nil {
		h.t.Fatal("no frame")
	}
	return f
}

// closed waits for the socket to close and returns its code.
func (h *appHarness) closed() websocket.StatusCode {
	h.t.Helper()
	for {
		select {
		case <-h.frames:
		case err := <-h.end:
			return websocket.CloseStatus(err)
		case <-time.After(2 * time.Second):
			h.t.Fatal("the socket stayed open")
		}
	}
}

func (h *appHarness) tell(f *pb.AppFrame) {
	h.t.Helper()
	b, _ := proto.Marshal(f)
	if err := h.app.Write(context.Background(), websocket.MessageBinary, b); err != nil {
		h.t.Fatal(err)
	}
}

func TestAppGetsReadyAndBareEvents(t *testing.T) {
	h := newAppHarness(t, appDeps(1<<20))
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	h.engine.Send(h.session, Ready{User: &pb.User{Id: "me"}, SessionID: "s", ExpiresAt: expires})
	r := h.must().GetReady()
	if r.GetUser().GetId() != "me" || r.SessionId != "s" || !r.ExpiresAt.AsTime().Equal(expires) || r.Resync {
		t.Fatalf("ready %v", r)
	}
	h.engine.Send(h.session, Event{Event: &pb.Event{ConnIds: []uint64{1, 2}, Payload: &pb.Event_Typing{Typing: &pb.Typing{ChannelId: chanID}}}})
	ev := h.must().GetEvent()
	if ev.GetTyping().GetChannelId() != chanID || len(ev.ConnIds) != 0 || ev.Broadcast {
		t.Fatalf("the app learned the event's recipients: %v", ev)
	}
}

func TestAppSocketsCloseWithTheirReason(t *testing.T) {
	cases := []struct {
		name string
		msg  any
		want websocket.StatusCode
	}{
		{"not a session", Rejected{}, CloseUnauthenticated},
		{"attach failed", Rejected{Retry: true}, CloseRetry},
		{"too many sockets", Rejected{TooMany: true}, CloseTooMany},
		{"revoked", Revoked{}, CloseRevoked},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newAppHarness(t, appDeps(1<<20))
			h.engine.Send(h.session, c.msg)
			if got := h.closed(); got != c.want {
				t.Fatalf("closed with %d, want %d", got, c.want)
			}
		})
	}
	t.Run("the session expires", func(t *testing.T) {
		h := newAppHarness(t, appDeps(1<<20))
		h.engine.Send(h.session, Ready{User: &pb.User{Id: "me"}, ExpiresAt: time.Now().Add(100 * time.Millisecond)})
		if got := h.closed(); got != CloseRevoked {
			t.Fatalf("closed with %d", got)
		}
	})
}

func TestASlowAppLosesItsSocket(t *testing.T) {
	h := newAppHarness(t, appDeps(1024))
	big := &pb.Message{Content: strings.Repeat("x", 4096)}
	h.engine.Send(h.session, Event{Event: &pb.Event{Payload: &pb.Event_MessagePosted{MessagePosted: &pb.MessagePosted{Message: big}}}})
	if got := h.closed(); got != CloseTooSlow {
		t.Fatalf("closed with %d", got)
	}
}

func TestAppFramesSayWhatTheAppShows(t *testing.T) {
	h := newAppHarness(t, appDeps(1<<20))
	h.engine.Send(h.session, Ready{User: &pb.User{Id: "me"}})
	h.must()
	// Garbage and a channel that is no id change nothing.
	if err := h.app.Write(context.Background(), websocket.MessageBinary, []byte{0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	h.tell(&pb.AppFrame{Frame: &pb.AppFrame_View{View: &pb.AppView{ChannelId: "../../etc"}}})
	h.tell(&pb.AppFrame{Frame: &pb.AppFrame_View{View: &pb.AppView{ChannelId: chanID}}})
	if v := routed[viewing](t, &harness{router: h.router}); v.channelID != chanID || v.connID != 1 {
		t.Fatalf("viewing %+v", v)
	}
	h.tell(&pb.AppFrame{Frame: &pb.AppFrame_View{View: &pb.AppView{}}})
	if v := routed[viewing](t, &harness{router: h.router}); v.channelID != "" {
		t.Fatalf("viewing nothing %+v", v)
	}
	// Watches keep valid ids once, up to the limit.
	peer := "44444444-4444-4444-4444-444444444444"
	h.tell(&pb.AppFrame{Frame: &pb.AppFrame_Watch{Watch: &pb.AppWatch{UserIds: []string{peer, "nope", peer, dmID}}}})
	if w := routed[watching](t, &harness{router: h.router}); !slices.Equal(w.users, []string{peer, dmID}) {
		t.Fatalf("watching %v", w.users)
	}
	many := make([]string, MaxWatched+50)
	for i := range many {
		many[i] = fmt.Sprintf("55555555-5555-5555-5555-%012x", i)
	}
	h.tell(&pb.AppFrame{Frame: &pb.AppFrame_Watch{Watch: &pb.AppWatch{UserIds: many}}})
	if w := routed[watching](t, &harness{router: h.router}); len(w.users) != MaxWatched {
		t.Fatalf("watching %d people", len(w.users))
	}
}

func TestAppsResyncOnTheirTurn(t *testing.T) {
	deps := appDeps(1 << 20)
	// The turn is taken: the next one is an hour away.
	deps.Resyncs = rate.NewLimiter(rate.Every(time.Hour), 1)
	deps.Resyncs.Reserve()
	h := newAppHarness(t, deps)
	h.engine.Send(h.session, Ready{User: &pb.User{Id: "me"}})
	h.must()
	h.engine.Send(h.session, Ready{User: &pb.User{Id: "me"}, Again: true})
	// Events flow while the resync waits.
	h.engine.Send(h.session, Event{Event: &pb.Event{Payload: &pb.Event_Typing{Typing: &pb.Typing{ChannelId: chanID}}}})
	if f := h.must(); f.GetEvent() == nil {
		t.Fatalf("got %v before the event", f)
	}
	if f := h.frame(200 * time.Millisecond); f != nil {
		t.Fatalf("the resync came before its turn: %v", f)
	}
	// On its turn the app is told to load again.
	h.engine.Send(h.session, resyncDue{})
	if r := h.must().GetReady(); r == nil || !r.Resync {
		t.Fatalf("no resync")
	}
}
