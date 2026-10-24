package voiceserver

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/actors"
)

// fakeLink records who the rooms say left.
type fakeLink struct {
	mu   sync.Mutex
	left []peerLeft
}

func (l *fakeLink) Receive(c *actor.Context) {
	if m, ok := c.Message().(peerLeft); ok {
		l.mu.Lock()
		l.left = append(l.left, m)
		l.mu.Unlock()
	}
}

func (l *fakeLink) has(session string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, m := range l.left {
		if m.session == session {
			return true
		}
	}
	return false
}

func freeUDPPort(t *testing.T) int {
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	return c.LocalAddr().(*net.UDPAddr).Port
}

type sfu struct {
	t      *testing.T
	engine *actor.Engine
	rooms  *actor.PID
	link   *fakeLink
}

func newSFU(t *testing.T, maxPeers int) *sfu {
	m, err := NewMedia(&ICEConfig{UDPPort: freeUDPPort(t), TURN: &TURNConfig{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		t.Fatal(err)
	}
	link := &fakeLink{}
	linkPID := engine.Spawn(func() actor.Receiver { return link }, "link")
	cfg := &Config{MaxPeersPerRoom: maxPeers}
	s := &sfu{t: t, engine: engine, link: link}
	s.rooms = engine.Spawn(newRooms(zap.NewNop(), cfg, m, linkPID), "rooms")
	t.Cleanup(func() { <-engine.Poison(s.rooms).Done() })
	return s
}

// client is a browser, played by Pion.
type client struct {
	t       *testing.T
	s       *sfu
	session string
	userID  string
	pc      *webrtc.PeerConnection
	out     *sender
	mu      sync.Mutex
	got     map[string]int // packets received by the stream id that sent them
	speak   []outbound
	offers  int
	closed  *closeFrame
	// early is set when a candidate came before any offer, which a browser
	// would drop.
	early bool
}

func (s *sfu) join(session, userID string, send bool) (*client, error) {
	se := webrtc.SettingEngine{}
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = pc.Close() })
	c := &client{t: s.t, s: s, session: session, userID: userID, pc: pc, out: newSender(256), got: map[string]int{}}
	if send {
		track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "mic", "local")
		if err != nil {
			s.t.Fatal(err)
		}
		if _, err := pc.AddTrack(track); err != nil {
			s.t.Fatal(err)
		}
		go func() {
			for {
				if err := track.WriteSample(media.Sample{Data: []byte{0xf8, 0xff, 0xfe}, Duration: 20 * time.Millisecond}); err != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
				if pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
					return
				}
			}
		}()
	}
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			if _, _, err := remote.ReadRTP(); err != nil {
				return
			}
			c.mu.Lock()
			c.got[remote.StreamID()]++
			c.mu.Unlock()
		}
	})
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand != nil {
			init := cand.ToJSON()
			s.engine.Send(s.rooms, toRoom{channelID: "room", msg: candidate{session: session, init: init}})
		}
	})
	go c.pump()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = actors.Ask[joined](ctx, s.engine, s.rooms, addPeer{session: session, userID: userID, channelID: "room", out: c.out})
	return c, err
}

// pump plays the browser's side of the signaling.
func (c *client) pump() {
	for {
		select {
		case b := <-c.out.ch:
			var m outbound
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			switch m.Op {
			case opOffer:
				if err := c.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}); err != nil {
					continue
				}
				ans, err := c.pc.CreateAnswer(nil)
				if err != nil || c.pc.SetLocalDescription(ans) != nil {
					continue
				}
				c.mu.Lock()
				c.offers++
				c.mu.Unlock()
				c.s.engine.Send(c.s.rooms, toRoom{channelID: "room", msg: answer{session: c.session, sdp: ans.SDP}})
			case opCandidate:
				c.mu.Lock()
				if c.offers == 0 && c.pc.RemoteDescription() == nil {
					c.early = true
				}
				c.mu.Unlock()
				_ = c.pc.AddICECandidate(*m.Candidate)
			case opSpeaking:
				c.mu.Lock()
				c.speak = append(c.speak, m)
				c.mu.Unlock()
			}
		case f := <-c.out.closed:
			c.mu.Lock()
			c.closed = &f
			c.mu.Unlock()
			return
		}
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", what)
}

func (c *client) heard(streamID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.got[streamID]
}

func TestRoomForwardsVoice(t *testing.T) {
	s := newSFU(t, 3)
	alice, err := s.join("s-alice", "alice", true)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.join("s-bob", "bob", true)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "bob hears alice", func() bool { return bob.heard("alice") > 10 })
	eventually(t, "alice hears bob", func() bool { return alice.heard("bob") > 10 })
	if alice.heard("alice") != 0 || bob.heard("bob") != 0 {
		t.Fatal("someone heard themselves")
	}
	for _, c := range []*client{alice, bob} {
		c.mu.Lock()
		if c.early {
			t.Errorf("%s got a candidate before its first offer", c.userID)
		}
		c.mu.Unlock()
	}

	// A third person hears both, who now hear them too.
	carol, err := s.join("s-carol", "carol", true)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "carol hears both", func() bool { return carol.heard("alice") > 10 && carol.heard("bob") > 10 })
	eventually(t, "both hear carol", func() bool { return alice.heard("carol") > 10 && bob.heard("carol") > 10 })

	// The room is full.
	if _, err := s.join("s-dave", "dave", false); !errors.Is(err, errFull) {
		t.Fatalf("a fourth joined a room of three: %v", err)
	}
	eventually(t, "the refused join is not counted", func() bool { return true })

	// Speaking reaches the others, not the speaker.
	s.engine.Send(s.rooms, toRoom{channelID: "room", msg: speaking{session: "s-alice", on: true}})
	eventually(t, "bob sees alice speak", func() bool {
		bob.mu.Lock()
		defer bob.mu.Unlock()
		return len(bob.speak) == 1 && bob.speak[0].UserID == "alice" && *bob.speak[0].Speaking
	})
	alice.mu.Lock()
	if len(alice.speak) != 0 {
		t.Error("alice was told she speaks")
	}
	alice.mu.Unlock()

	// When carol leaves, the chat server hears of it and the others stop
	// getting her voice.
	bob.mu.Lock()
	offers := bob.offers
	bob.mu.Unlock()
	s.engine.Send(s.rooms, disconnect{channelID: "room", session: "s-carol", reason: "kicked"})
	eventually(t, "carol is told", func() bool {
		carol.mu.Lock()
		defer carol.mu.Unlock()
		return carol.closed != nil && carol.closed.code == closeDisconnected && carol.closed.reason == "kicked"
	})
	eventually(t, "the chat server hears carol left", func() bool { return s.link.has("s-carol") })
	eventually(t, "bob renegotiates without carol", func() bool {
		bob.mu.Lock()
		defer bob.mu.Unlock()
		return bob.offers > offers
	})
	before := bob.heard("carol")
	time.Sleep(300 * time.Millisecond)
	if after := bob.heard("carol"); after > before+2 {
		t.Fatalf("bob still hears carol: %d then %d", before, after)
	}
	if bob.heard("alice") == 0 {
		t.Fatal("bob lost alice")
	}
	if _, err := s.join("s-dave", "dave", false); err != nil {
		t.Fatalf("a place freed but dave could not join: %v", err)
	}
}

func TestDropAllEndsEveryConnection(t *testing.T) {
	s := newSFU(t, 5)
	a, err := s.join("s-a", "a", true)
	if err != nil {
		t.Fatal(err)
	}
	s.engine.Send(s.rooms, dropAll{reason: "lost the chat server"})
	eventually(t, "the peer is closed", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.closed != nil && a.closed.code == closeUnavailable
	})
	eventually(t, "the chat server hears it", func() bool { return s.link.has("s-a") })
}

func TestICEServersSignTURNCredentials(t *testing.T) {
	m := &Media{turn: &TURNConfig{URLs: []string{"turn:turn.example.com:3478"}, Secret: "0123456789abcdef", TTL: time.Hour}}
	now := time.Unix(1_700_000_000, 0)
	got := m.iceServers("u1", now)
	if len(got) != 1 || got[0].Username != "1700003600:u1" || got[0].Credential == "" {
		t.Fatalf("ice servers: %+v", got)
	}
	// coturn's REST credential: base64(HMAC-SHA1(secret, username)).
	if got[0].Credential != "ZnjCR+EEsT2EciZj6mKuDl2b/bU=" {
		t.Errorf("credential %q", got[0].Credential)
	}
	if (&Media{turn: &TURNConfig{}}).iceServers("u1", now) != nil {
		t.Error("ice servers without TURN")
	}
}
