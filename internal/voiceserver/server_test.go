package voiceserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/coder/websocket"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/client/drpc"
)

func testServer(t *testing.T, maxUnidentified int) *httptest.Server {
	cfg := &Config{
		ID: "test", PublicURL: "ws://localhost/rtc", AllowedOrigins: []string{"http://chat.test"},
		HTTP: &HTTPConfig{Address: "127.0.0.1:0", ReadHeaderTimeout: time.Second, ShutdownTimeout: time.Second},
		ICE:  &ICEConfig{TURN: &TURNConfig{}}, MaxPeersPerRoom: 2, IdentifyTimeout: 2 * time.Second, PingInterval: time.Minute,
		WriteTimeout: time.Second, ReadLimit: 1 << 16, SendQueue: 8, ReconnectMin: time.Hour, ReconnectMax: time.Hour,
		MaxUnidentified: maxUnidentified, MessageRate: 10, MessageBurst: 10,
	}
	client, err := drpc.New(&drpc.Config{Address: "127.0.0.1:1", GatewayKey: strings.Repeat("k", 32), TLS: &drpc.TLSConfig{},
		DialTimeout: time.Second, WriteTimeout: time.Second, RequestTimeout: time.Second, PoolCapacity: 1, PoolExpiration: time.Minute, MaxMessageSize: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		t.Fatal(err)
	}
	s := New(zap.NewNop(), cfg, client, engine, &Media{})
	ts := httptest.NewServer(s.srv.Handler)
	t.Cleanup(func() {
		ts.Close()
		s.Stop()
	})
	return ts
}

func dial(ts *httptest.Server, origin string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", origin)
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/rtc", &websocket.DialOptions{HTTPHeader: h})
}

func TestSignalingRefusesOtherOrigins(t *testing.T) {
	ts := testServer(t, 4)
	if c, _, err := dial(ts, "http://evil.test"); err == nil {
		_ = c.CloseNow()
		t.Fatal("another origin connected")
	}
}

func TestUnidentifiedConnectionsAreBounded(t *testing.T) {
	ts := testServer(t, 1)
	first, _, err := dial(ts, "http://chat.test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.CloseNow() }()
	if c, res, err := dial(ts, "http://chat.test"); err == nil {
		_ = c.CloseNow()
		t.Fatal("a second unidentified connection was taken")
	} else if res == nil || res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("refusal: %v %v", res, err)
	}
	// A connection that never identifies is closed, and frees its place.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := first.Read(ctx); websocket.CloseStatus(err) != closeIdentify {
		t.Fatalf("not closed for want of identify: %v", err)
	}
	// The place frees once the server's read returns, a moment after the
	// browser sees the close.
	for {
		c, _, err := dial(ts, "http://chat.test")
		if err == nil {
			_ = c.CloseNow()
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("the place was not freed: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
