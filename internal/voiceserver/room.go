package voiceserver

import (
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"github.com/ssenerg/gochat/internal/actors"
)

// Messages for a room's actor. Session names one browser's connection.
type (
	// addPeer replies with joined or an error.
	addPeer struct {
		session, userID, channelID string
		out                        *sender
	}
	joined     struct{}
	removePeer struct {
		session string
		code    int
		reason  string
	}
	answer struct {
		session, sdp string
	}
	candidate struct {
		session string
		init    webrtc.ICECandidateInit
	}
	speaking struct {
		session string
		on      bool
	}
	trackAdded struct {
		session string
		track   *webrtc.TrackLocalStaticRTP
	}
	trackRemoved struct {
		session, id string
	}
	pcState struct {
		session string
		state   webrtc.PeerConnectionState
	}
	resync struct{}
)

// peerGone tells the rooms actor a peer left a room.
type peerGone struct{ channelID string }

type peer struct {
	session, userID string
	pc              *webrtc.PeerConnection
	out             *sender
	speak           *rate.Limiter
	// needOffer is set when the peer's tracks changed while it could not
	// take an offer, or before its first.
	needOffer bool
}

// room forwards the voice of each peer of one voice channel to the others,
// as an SFU: it neither mixes nor decodes.
type room struct {
	logger    *zap.Logger
	cfg       *Config
	media     *Media
	link      *actor.PID
	rooms     *actor.PID
	channelID string
	peers     map[string]*peer
	// tracks are the peers' voices as the others receive them, by id;
	// owners name whose each is.
	tracks map[string]*webrtc.TrackLocalStaticRTP
	owners map[string]string
	retry  bool
}

func newRoom(logger *zap.Logger, cfg *Config, media *Media, link, rooms *actor.PID, channelID string) actor.Producer {
	return func() actor.Receiver {
		return &room{
			logger: logger, cfg: cfg, media: media, link: link, rooms: rooms, channelID: channelID,
			peers:  make(map[string]*peer),
			tracks: make(map[string]*webrtc.TrackLocalStaticRTP),
			owners: make(map[string]string),
		}
	}
}

func (r *room) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Stopped:
		for _, p := range r.peers {
			r.drop(c, p, closeUnavailable, "the voice server is stopping")
		}
	case addPeer:
		if err := r.add(c, msg); err != nil {
			actors.Reply(c, err)
			c.Send(r.rooms, peerGone{channelID: r.channelID})
			return
		}
		actors.Reply(c, joined{})
	case removePeer:
		if p := r.peers[msg.session]; p != nil {
			r.drop(c, p, msg.code, msg.reason)
			r.signal(c)
		}
	case answer:
		r.answer(c, msg)
	case candidate:
		if p := r.peers[msg.session]; p != nil {
			if err := p.pc.AddICECandidate(msg.init); err != nil {
				r.logger.Debug("bad candidate", zap.String("session", msg.session), zap.Error(err))
			}
		}
	case speaking:
		r.speaking(msg)
	case trackAdded:
		if r.peers[msg.session] == nil {
			return
		}
		r.tracks[msg.track.ID()] = msg.track
		r.owners[msg.track.ID()] = msg.session
		r.signal(c)
	case trackRemoved:
		if r.owners[msg.id] == msg.session {
			delete(r.tracks, msg.id)
			delete(r.owners, msg.id)
			r.signal(c)
		}
	case pcState:
		if p := r.peers[msg.session]; p != nil && (msg.state == webrtc.PeerConnectionStateFailed || msg.state == webrtc.PeerConnectionStateClosed) {
			r.drop(c, p, closeFailed, "the connection failed")
			r.signal(c)
		}
	case resync:
		r.retry = false
		r.signal(c)
	case dropPeers:
		for _, p := range r.peers {
			r.drop(c, p, closeUnavailable, msg.reason)
		}
	}
}

func (r *room) add(c *actor.Context, msg addPeer) error {
	if len(r.peers) >= r.cfg.MaxPeersPerRoom {
		return errFull
	}
	pc, err := r.media.newPeerConnection()
	if err != nil {
		return err
	}
	// The peer's own voice comes in on this transceiver.
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		_ = pc.Close()
		return err
	}
	p := &peer{session: msg.session, userID: msg.userID, pc: pc, out: msg.out, speak: rate.NewLimiter(10, 20), needOffer: true}
	self, engine := c.PID(), c.Engine()
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand == nil {
			return
		}
		init := cand.ToJSON()
		p.out.send(outbound{Op: opCandidate, Candidate: &init})
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		engine.Send(self, pcState{session: p.session, state: s})
	})
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		forward(engine, self, p.session, p.userID, remote)
	})
	r.peers[p.session] = p
	p.out.send(outbound{Op: opReady, UserID: p.userID, ChannelID: r.channelID, Session: p.session, ICEServers: r.media.iceServers(p.userID, time.Now())})
	r.signal(c)
	return nil
}

var errFull = errors.New("the voice channel is full")

// forward relays a peer's voice to the track the others receive, until the
// peer goes. It runs on Pion's goroutine for the track.
func forward(engine *actor.Engine, room *actor.PID, session, userID string, remote *webrtc.TrackRemote) {
	if remote.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}
	// The stream id tells receivers whose voice it is.
	local, err := webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, "voice-"+session, userID)
	if err != nil {
		return
	}
	engine.Send(room, trackAdded{session: session, track: local})
	defer engine.Send(room, trackRemoved{session: session, id: local.ID()})
	buf := make([]byte, 1500)
	for {
		n, _, err := remote.Read(buf)
		if err != nil {
			return
		}
		if _, err := local.Write(buf[:n]); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			return
		}
	}
}

func (r *room) answer(c *actor.Context, msg answer) {
	p := r.peers[msg.session]
	if p == nil {
		return
	}
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: msg.sdp}); err != nil {
		r.logger.Debug("bad answer", zap.String("session", msg.session), zap.Error(err))
		r.drop(c, p, closeFailed, "the browser's answer was refused")
		r.signal(c)
		return
	}
	if p.needOffer {
		r.signal(c)
	}
}

func (r *room) speaking(msg speaking) {
	p := r.peers[msg.session]
	if p == nil || !p.speak.Allow() {
		return
	}
	on := msg.on
	out := outbound{Op: opSpeaking, UserID: p.userID, Speaking: &on}
	for _, other := range r.peers {
		if other != p {
			other.out.send(out)
		}
	}
}

// drop ends a peer's connection and tells the chat server it left.
func (r *room) drop(c *actor.Context, p *peer, code int, reason string) {
	delete(r.peers, p.session)
	for id, owner := range r.owners {
		if owner == p.session {
			delete(r.tracks, id)
			delete(r.owners, id)
		}
	}
	_ = p.pc.Close()
	p.out.close(code, reason)
	c.Send(r.link, peerLeft{userID: p.userID, channelID: r.channelID, session: p.session})
	c.Send(r.rooms, peerGone{channelID: r.channelID})
}

// signal brings every peer's outgoing tracks in line with the others'
// voices, and offers the change to those it changed. A peer in the middle
// of a negotiation gets its offer once it answers.
func (r *room) signal(c *actor.Context) {
	for range 25 {
		if !r.syncOnce(c) {
			return
		}
	}
	// Pion refused a change: try again in a while rather than spin.
	if !r.retry {
		r.retry = true
		self, engine := c.PID(), c.Engine()
		time.AfterFunc(3*time.Second, func() { engine.Send(self, resync{}) })
	}
}

// syncOnce reports whether it must be run again.
func (r *room) syncOnce(c *actor.Context) bool {
	for _, p := range r.peers {
		if p.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
			r.drop(c, p, closeFailed, "the connection closed")
			return true
		}
		changed := false
		have := make(map[string]bool)
		for _, s := range p.pc.GetSenders() {
			t := s.Track()
			if t == nil {
				continue
			}
			have[t.ID()] = true
			if _, ok := r.tracks[t.ID()]; !ok {
				if err := p.pc.RemoveTrack(s); err != nil {
					return true
				}
				changed = true
			}
		}
		for id, t := range r.tracks {
			if have[id] || r.owners[id] == p.session {
				continue
			}
			if _, err := p.pc.AddTrack(t); err != nil {
				return true
			}
			changed = true
		}
		if !changed && !p.needOffer {
			continue
		}
		if p.pc.SignalingState() != webrtc.SignalingStateStable {
			p.needOffer = true
			continue
		}
		offer, err := p.pc.CreateOffer(nil)
		if err != nil {
			return true
		}
		if err := p.pc.SetLocalDescription(offer); err != nil {
			return true
		}
		p.needOffer = false
		p.out.send(outbound{Op: opOffer, SDP: offer.SDP})
	}
	return false
}

// sender queues messages for one browser's websocket; a browser that
// falls behind is cut off.
type sender struct {
	ch     chan []byte
	closed chan closeFrame
}

type closeFrame struct {
	code   int
	reason string
}

func newSender(queue int) *sender {
	return &sender{ch: make(chan []byte, queue), closed: make(chan closeFrame, 1)}
}

// send is called from the room's actor and Pion's goroutines.
func (s *sender) send(msg outbound) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case s.ch <- b:
	default:
		s.close(closeFailed, "the browser is not keeping up")
	}
}

// close asks the websocket's writer to close it, once.
func (s *sender) close(code int, reason string) {
	select {
	case s.closed <- closeFrame{code: code, reason: reason}:
	default:
	}
}
