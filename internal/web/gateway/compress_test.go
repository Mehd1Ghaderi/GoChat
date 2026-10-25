package gateway

import (
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/web/render"
)

// countingConn counts the bytes a client reads off the wire.
type countingConn struct {
	net.Conn
	read *atomic.Int64
}

func (c countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.read.Add(int64(n))
	return n, err
}

// sidebarFragment renders a sidebar of 30 channels and 10 conversations.
func sidebarFragment(t testing.TB) []byte {
	t.Helper()
	r, err := render.New(100)
	if err != nil {
		t.Fatal(err)
	}
	lists := &render.Lists{Team: &pb.Team{Id: teamID, Name: "acme"}, OOB: true}
	for i := range 30 {
		lists.Channels = append(lists.Channels, &pb.Channel{Id: fmt.Sprintf("%08d-1111-1111-1111-111111111111", i), Name: fmt.Sprintf("channel-%d", i), DisplayName: fmt.Sprintf("Channel %d", i)})
	}
	for i := range 10 {
		lists.DMs = append(lists.DMs, &pb.Channel{
			Id: fmt.Sprintf("%08d-2222-2222-2222-222222222222", i), Type: pb.ChannelType_CHANNEL_TYPE_DIRECT,
			Peer: &pb.User{Id: fmt.Sprintf("%08d-3333-3333-3333-333333333333", i), Username: fmt.Sprintf("person%d", i), DisplayName: fmt.Sprintf("Person %d", i)},
		})
	}
	b, err := r.FragmentBytes("channel-lists", lists)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// serveSocket accepts sockets with cfg, writes them msg and reports what
// it reads from them.
func serveSocket(t *testing.T, cfg *Config, msg []byte) (string, chan error) {
	t.Helper()
	read := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, cfg.AcceptOptions(r.Host))
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		conn.SetReadLimit(cfg.ReadLimit)
		if err := conn.Write(context.Background(), websocket.MessageText, msg); err != nil {
			return
		}
		_, _, err = conn.Read(context.Background())
		read <- err
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), read
}

func dialCounting(t *testing.T, url string) (*websocket.Conn, *http.Response, *atomic.Int64) {
	t.Helper()
	read := new(atomic.Int64)
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		return countingConn{Conn: c, read: read}, err
	}}}
	conn, res, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPClient: client, CompressionMode: websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn, res, read
}

func TestSocketsCompress(t *testing.T) {
	sidebar := sidebarFragment(t)
	cfg := testConfig()
	cfg.ReadLimit, cfg.Compress, cfg.CompressThreshold = 1<<16, true, 512
	url, _ := serveSocket(t, cfg, sidebar)
	conn, res, wire := dialCounting(t, url)
	if ext := res.Header.Get("Sec-WebSocket-Extensions"); !strings.Contains(ext, "permessage-deflate") || !strings.Contains(ext, "server_no_context_takeover") {
		t.Fatalf("negotiated %q", ext)
	}
	// Counted from the dial: reading the handshake may take the frame's
	// first bytes too.
	_, got, err := conn.Read(context.Background())
	if err != nil || !bytes.Equal(got, sidebar) {
		t.Fatalf("read %d bytes, %v", len(got), err)
	}
	if sent := wire.Load(); sent > int64(len(sidebar))/3 {
		t.Fatalf("%d bytes of sidebar took %d on the wire", len(sidebar), sent)
	}
	// Without compression, the same bytes go as they are.
	cfg.Compress = false
	url, _ = serveSocket(t, cfg, sidebar)
	conn, res, wire = dialCounting(t, url)
	if ext := res.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		t.Fatalf("negotiated %q with compression off", ext)
	}
	if _, _, err := conn.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent := wire.Load(); sent < int64(len(sidebar)) {
		t.Fatalf("%d bytes of sidebar took %d on the wire uncompressed", len(sidebar), sent)
	}
}

func TestReadLimitCountsInflatedBytes(t *testing.T) {
	cfg := testConfig()
	cfg.ReadLimit, cfg.Compress, cfg.CompressThreshold = 1<<10, true, 512
	url, read := serveSocket(t, cfg, []byte("hi"))
	conn, _, _ := dialCounting(t, url)
	if _, _, err := conn.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A megabyte of zeros deflates to a few hundred bytes.
	_ = conn.Write(context.Background(), websocket.MessageText, make([]byte, 1<<20))
	if err := <-read; !errors.Is(err, websocket.ErrMessageTooBig) {
		t.Fatalf("the server read a compressed megabyte: %v", err)
	}
}

// BenchmarkCompressFragments deflates fragments one at a time, as sockets
// send them.
func BenchmarkCompressFragments(b *testing.B) {
	r, err := render.New(100)
	if err != nil {
		b.Fatal(err)
	}
	m := r.Message(&pb.Message{
		Id: "0b6e8e4a-6f1d-11ef-8000-000000000001", ChannelId: chanID,
		Content: "Shipping the release notes now: see the **changelog** for what moved, and ping me with questions.",
		Author:  &pb.User{Id: "00000001-3333-3333-3333-333333333333", Username: "ada", DisplayName: "Ada Lovelace"},
	}, &pb.User{Id: "me", Username: "me"}, false)
	message, err := r.MessageBytes("message-new", m)
	if err != nil {
		b.Fatal(err)
	}
	for _, c := range []struct {
		name string
		data []byte
	}{{"message", message}, {"sidebar", sidebarFragment(b)}} {
		b.Run(c.name, func(b *testing.B) {
			var buf bytes.Buffer
			w, _ := flate.NewWriter(&buf, flate.BestSpeed)
			b.SetBytes(int64(len(c.data)))
			for range b.N {
				buf.Reset()
				w.Reset(&buf)
				_, _ = w.Write(c.data)
				_ = w.Flush()
			}
			b.ReportMetric(float64(len(c.data)), "raw-bytes")
			b.ReportMetric(float64(buf.Len()), "wire-bytes")
		})
	}
}
