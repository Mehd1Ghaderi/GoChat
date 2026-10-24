package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"github.com/ssenerg/gochat/internal/core/secure"
)

// voicePeer is a browser in a voice channel, played by Pion: it identifies
// at the voice server, answers its offers, sends a voice if asked to and
// counts the packets of each voice it receives.
type voicePeer struct {
	t      *testing.T
	ws     *websocket.Conn
	pc     *webrtc.PeerConnection
	mu     sync.Mutex
	userID string
	heard  map[string]int
	closed chan websocket.CloseError
}

type voiceTicket struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
}

func (c *client) voiceTicket(channelID string) voiceTicket {
	c.t.Helper()
	res, body := c.hx("/voice/join", url.Values{"channel_id": {channelID}})
	var tk voiceTicket
	if res.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &tk) != nil || tk.Token == "" {
		c.t.Fatalf("join voice: %d %s %s", res.StatusCode, res.Header.Get("X-Error"), body)
	}
	return tk
}

func dialVoice(endpoint, origin string) (*websocket.Conn, error) {
	h := http.Header{}
	h.Set("Origin", origin)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: h})
	return conn, err
}

func joinVoice(t *testing.T, c *client, channelID string) *voicePeer {
	t.Helper()
	tk := c.voiceTicket(channelID)
	ws, err := dialVoice(tk.Endpoint, baseURL)
	if err != nil {
		t.Fatalf("voice websocket: %v", err)
	}
	se := webrtc.SettingEngine{}
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	p := &voicePeer{t: t, ws: ws, pc: pc, heard: map[string]int{}, closed: make(chan websocket.CloseError, 1)}
	t.Cleanup(func() {
		_ = pc.Close()
		_ = ws.CloseNow()
	})
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "mic", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.AddTrack(track); err != nil {
		t.Fatal(err)
	}
	go func() {
		for pc.ConnectionState() != webrtc.PeerConnectionStateClosed {
			if track.WriteSample(media.Sample{Data: []byte{0xf8, 0xff, 0xfe}, Duration: 20 * time.Millisecond}) != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			if _, _, err := remote.ReadRTP(); err != nil {
				return
			}
			p.mu.Lock()
			p.heard[remote.StreamID()]++
			p.mu.Unlock()
		}
	})
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand != nil {
			init := cand.ToJSON()
			p.send(map[string]any{"op": "candidate", "candidate": init})
		}
	})
	p.send(map[string]any{"op": "identify", "token": tk.Token})
	go p.read()
	return p
}

func (p *voicePeer) send(v any) {
	b, _ := json.Marshal(v)
	_ = p.ws.Write(context.Background(), websocket.MessageText, b)
}

func (p *voicePeer) read() {
	for {
		_, data, err := p.ws.Read(context.Background())
		if err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				p.closed <- ce
			}
			close(p.closed)
			return
		}
		var m struct {
			Op        string                   `json:"op"`
			UserID    string                   `json:"user_id"`
			SDP       string                   `json:"sdp"`
			Candidate *webrtc.ICECandidateInit `json:"candidate"`
		}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Op {
		case "ready":
			p.mu.Lock()
			p.userID = m.UserID
			p.mu.Unlock()
		case "offer":
			if p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}) != nil {
				continue
			}
			ans, err := p.pc.CreateAnswer(nil)
			if err != nil || p.pc.SetLocalDescription(ans) != nil {
				continue
			}
			p.send(map[string]any{"op": "answer", "sdp": ans.SDP})
		case "candidate":
			if m.Candidate != nil {
				_ = p.pc.AddICECandidate(*m.Candidate)
			}
		}
	}
}

func (p *voicePeer) id() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.userID
}

func (p *voicePeer) heardFrom(userID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.heard[userID]
}

func (p *voicePeer) waitFor(what string, ok func() bool) {
	p.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	p.t.Fatalf("timed out waiting: %s", what)
}

// closedWith waits for the voice server to close the connection.
func (p *voicePeer) closedWith() websocket.CloseError {
	p.t.Helper()
	select {
	case ce, ok := <-p.closed:
		if !ok {
			p.t.Fatal("the voice connection ended without a close frame")
		}
		return ce
	case <-time.After(10 * time.Second):
		p.t.Fatal("the voice connection stayed open")
	}
	return websocket.CloseError{}
}

// testVoiceChannels runs with admin and bob signed in, their sockets showing
// a text channel of the team.
func testVoiceChannels(t *testing.T, admin, bob *client, adminWS, bobWS *socket) {
	name := "talk-" + random(3)
	res, body := admin.hx("/t/"+teamName+"/channels", url.Values{"name": {name}, "display_name": {"Talk"}, "type": {"voice"}})
	m := channelRe.FindStringSubmatch(res.Header.Get("HX-Location"))
	if m == nil {
		t.Fatalf("create voice channel: %d %s", res.StatusCode, body)
	}
	room := m[1]
	home, _ := bob.get("/t/" + teamName)
	if _, page := bob.get(home.Header.Get("Location")); !strings.Contains(page, `data-voice-join="`+room+`"`) {
		t.Fatal("bob's sidebar lacks the new voice channel")
	}

	// The voice server takes only the web gateway's pages, and only
	// with a token.
	tk := admin.voiceTicket(room)
	if conn, err := dialVoice(tk.Endpoint, "http://evil.example"); err == nil {
		_ = conn.CloseNow()
		t.Fatal("the voice server took a page of another origin")
	}
	bad, err := dialVoice(tk.Endpoint, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = bad.Write(context.Background(), websocket.MessageText, []byte(`{"op":"identify","token":"`+secure.Token()+`"}`))
	if _, _, err := bad.Read(context.Background()); websocket.CloseStatus(err) != 4001 {
		t.Fatalf("an unknown token was taken: %v", err)
	}

	a := joinVoice(t, admin, room)
	b := joinVoice(t, bob, room)
	a.waitFor("both are ready", func() bool { return a.id() != "" && b.id() != "" })
	a.waitFor("admin hears bob", func() bool { return a.heardFrom(b.id()) > 10 })
	b.waitFor("bob hears admin", func() bool { return b.heardFrom(a.id()) > 10 })
	if a.heardFrom(a.id()) != 0 {
		t.Fatal("admin hears herself")
	}
	adminWS.expect(10*time.Second, `id="voice-members-`+room+`"`, `data-user="`+b.id()+`"`, `data-user="`+a.id()+`"`)

	if res, _ := bob.hx("/voice/state", url.Values{"self_mute": {"1"}}); res.StatusCode != http.StatusNoContent {
		t.Fatalf("mute: %d", res.StatusCode)
	}
	adminWS.expect(5*time.Second, `id="voice-members-`+room+`"`, `title="Muted"`)

	// Only moderators disconnect others.
	if res, _ := bob.hx("/t/"+teamName+"/ch/"+room+"/voice/"+a.id()+"/disconnect", url.Values{}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("bob disconnected admin: %d", res.StatusCode)
	}
	if res, _ := admin.hx("/t/"+teamName+"/ch/"+room+"/voice/"+b.id()+"/disconnect", url.Values{}); res.StatusCode != http.StatusNoContent {
		t.Fatalf("admin could not disconnect bob: %d", res.StatusCode)
	}
	if ce := b.closedWith(); ce.Code != 4002 || ce.Reason != "disconnected by a moderator" {
		t.Fatalf("bob's close: %d %q", ce.Code, ce.Reason)
	}
	bobWS.expect(5*time.Second, `id="voice-members-`+room+`"`)

	// A voice channel carries no messages.
	adminWS.send(map[string]string{"op": "post", "channel_id": room, "content": "hello"})
	adminWS.expect(5*time.Second, "voice channels carry no messages")

	if res, _ := admin.hx("/voice/leave", url.Values{}); res.StatusCode != http.StatusNoContent {
		t.Fatalf("leave: %d", res.StatusCode)
	}
	if ce := a.closedWith(); ce.Code != 4002 {
		t.Fatalf("admin's close: %d %q", ce.Code, ce.Reason)
	}
}
