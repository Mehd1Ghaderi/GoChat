package gateway

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/coder/websocket"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
	"github.com/ssenerg/gochat/internal/web/render"
)

const (
	typingLinger = 6 * time.Second
	readDelay    = time.Second
)

var (
	uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,49}$`)
)

type Deps struct {
	Logger *zap.Logger
	Cfg    *Config
	Client *drpc.Client
	Render *render.Renderer
	Router *actor.PID
}

// Frame is one message a browser sent over its socket.
type Frame struct{ Data []byte }

type outMsg struct {
	data  []byte
	code  websocket.StatusCode
	close bool
}

type (
	clearTyping struct {
		channelID string
		seq       uint64
	}
	flushReads   struct{}
	sidebarReady struct {
		teamID string
		lists  *render.Lists
	}
	railReady struct{ rail *render.Rail }
	failed    struct{ text string }
)

type session struct {
	*Deps
	connID    uint64
	call      drpc.Call
	ws        *websocket.Conn
	out       chan outMsg
	limiter   *rate.Limiter
	user      *pb.User
	sessionID string
	teamID    string
	teamName  string
	channelID string
	moderator bool
	sidebar   map[string]bool
	peers     map[string]bool
	typingSeq uint64
	reads     map[string]string
	flushing  bool
	closing   bool
	stopPing  context.CancelFunc
	// early holds what the browser sent before the server attached it.
	early [][]byte
}

func NewSession(d *Deps, connID uint64, call drpc.Call, ws *websocket.Conn) actor.Producer {
	return func() actor.Receiver {
		return &session{
			Deps:    d,
			connID:  connID,
			call:    call,
			ws:      ws,
			out:     make(chan outMsg, d.Cfg.SendQueue),
			limiter: rate.NewLimiter(rate.Limit(d.Cfg.FrameRate), d.Cfg.FrameBurst),
			sidebar: make(map[string]bool),
			peers:   make(map[string]bool),
			reads:   make(map[string]string),
		}
	}
}

func (s *session) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Started:
		go s.write()
		ctx, cancel := context.WithCancel(context.Background())
		s.stopPing = cancel
		go s.ping(ctx)
		c.Send(s.Router, register{connID: s.connID, token: s.call.Session, pid: c.PID()})
	case actor.Stopped:
		c.Send(s.Router, unregister{connID: s.connID})
		s.stopPing()
		if !s.closing {
			s.closing = true
			close(s.out)
		}
	case Ready:
		s.user, s.sessionID = msg.User, msg.SessionID
		for _, data := range s.early {
			s.frame(c, data)
		}
		s.early = nil
		if msg.Again {
			s.resync(c)
		}
	case Rejected:
		if msg.TooMany {
			s.toast("error", "Too many open tabs: close one and reload.")
			s.close(websocket.StatusPolicyViolation)
			return
		}
		s.redirect("/login")
	case Revoked:
		s.redirect("/login")
	case Event:
		if s.user != nil {
			s.event(c, msg.Event)
		}
	case Frame:
		if !s.limiter.Allow() {
			s.close(websocket.StatusPolicyViolation)
			return
		}
		if s.user != nil {
			s.frame(c, msg.Data)
		} else if len(s.early) < 8 {
			s.early = append(s.early, msg.Data)
		}
	case clearTyping:
		if msg.seq == s.typingSeq && msg.channelID == s.channelID {
			s.fragment("typing", render.Typing{ChannelID: msg.channelID})
		}
	case flushReads:
		s.flushing = false
		s.flush(c)
	case sidebarReady:
		if msg.teamID != s.teamID {
			return
		}
		s.sidebar = make(map[string]bool, len(msg.lists.Channels)+len(msg.lists.DMs))
		s.peers = make(map[string]bool, len(msg.lists.DMs))
		for _, ch := range msg.lists.Channels {
			s.sidebar[ch.Id] = true
		}
		for _, ch := range msg.lists.DMs {
			s.sidebar[ch.Id] = true
			if ch.Peer != nil {
				s.peers[ch.Peer.Id] = true
			}
		}
		if msg.lists.OOB {
			msg.lists.Active = s.channelID
			s.fragment("channel-lists", msg.lists)
		}
	case railReady:
		s.fragment("team-rail", msg.rail)
	case failed:
		s.toast("error", msg.text)
	}
}

// write owns the socket's sending side.
func (s *session) write() {
	for m := range s.out {
		if m.close {
			_ = s.ws.Close(m.code, "")
			s.drain()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.Cfg.WriteTimeout)
		err := s.ws.Write(ctx, websocket.MessageText, m.data)
		cancel()
		if err != nil {
			_ = s.ws.CloseNow()
			s.drain()
			return
		}
	}
	_ = s.ws.Close(websocket.StatusNormalClosure, "")
}

func (s *session) drain() {
	for range s.out {
	}
}

func (s *session) ping(ctx context.Context) {
	t := time.NewTicker(s.Cfg.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, s.Cfg.WriteTimeout)
			err := s.ws.Ping(pctx)
			cancel()
			if err != nil {
				_ = s.ws.CloseNow()
				return
			}
		}
	}
}

// send queues data, closing a socket whose browser does not keep up.
func (s *session) send(data []byte) {
	if s.closing {
		return
	}
	select {
	case s.out <- outMsg{data: data}:
	default:
		s.Logger.Debug("browser too slow, closing its socket", zap.Uint64("conn", s.connID))
		_ = s.ws.CloseNow()
		s.closing = true
		close(s.out)
	}
}

func (s *session) close(code websocket.StatusCode) {
	if s.closing {
		return
	}
	select {
	case s.out <- outMsg{close: true, code: code}:
	default:
		_ = s.ws.CloseNow()
	}
	s.closing = true
	close(s.out)
}

func (s *session) fragment(name string, data any) {
	b, err := s.Render.FragmentBytes(name, data)
	if err != nil {
		s.Logger.Error("failed to render fragment", zap.String("fragment", name), zap.Error(err))
		return
	}
	s.send(b)
}

func (s *session) toast(kind, text string) {
	s.fragment("toast", render.Toast{Kind: kind, Text: text})
}

func (s *session) redirect(to string) {
	s.fragment("ws-control", to)
	s.close(websocket.StatusPolicyViolation)
}

func (s *session) channelURL() string {
	return "/t/" + s.teamName + "/ch/" + s.channelID
}

func (s *session) resync(c *actor.Context) {
	if s.channelID == "" || s.teamName == "" {
		return
	}
	s.fragment("resync", s.channelURL())
	s.loadSidebar(c, true)
}

// rpc runs fn off the actor; its result comes back as a message.
func (s *session) rpc(c *actor.Context, fn func(ctx context.Context) any) {
	self, engine := c.PID(), c.Engine()
	go func() {
		ctx, cancel := s.Client.Context(context.Background(), s.call)
		defer cancel()
		if v := fn(ctx); v != nil {
			engine.Send(self, v)
		}
	}()
}

func (s *session) loadSidebar(c *actor.Context, oob bool) {
	teamID, team := s.teamID, &pb.Team{Id: s.teamID, Name: s.teamName}
	s.rpc(c, func(ctx context.Context) any {
		res, err := s.Client.Channels.ListChannels(ctx, &pb.ListChannelsRequest{TeamId: teamID})
		if err != nil {
			return nil
		}
		channels, direct := render.Split(res.Channels)
		online := map[string]bool{}
		if peers := render.Peers(direct); len(peers) > 0 {
			if p, err := s.Client.Users.GetPresence(ctx, &pb.GetPresenceRequest{UserIds: peers}); err == nil {
				for _, id := range p.OnlineUserIds {
					online[id] = true
				}
			}
		}
		return sidebarReady{teamID: teamID, lists: &render.Lists{Team: team, Channels: channels, DMs: direct, Online: online, OOB: oob}}
	})
}

func (s *session) loadRail(c *actor.Context) {
	team := &pb.Team{Id: s.teamID, Name: s.teamName}
	s.rpc(c, func(ctx context.Context) any {
		res, err := s.Client.Teams.ListTeams(ctx, &pb.ListTeamsRequest{})
		if err != nil {
			return nil
		}
		return railReady{rail: &render.Rail{Teams: res.Teams, Team: team, OOB: true}}
	})
}

// clientFrame is what htmx's ws-send serializes: form fields, plus HEADERS.
type clientFrame struct {
	Op         string          `json:"op"`
	TeamID     string          `json:"team_id"`
	TeamName   string          `json:"team_name"`
	ChannelID  string          `json:"channel_id"`
	LastID     string          `json:"last_id"`
	Moderator  string          `json:"moderator"`
	Resync     string          `json:"resync"`
	Content    string          `json:"content"`
	Attachment json.RawMessage `json:"attachment"`
}

func (f *clientFrame) attachments() []string {
	if len(f.Attachment) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(f.Attachment, &one) == nil {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(f.Attachment, &many) == nil {
		return many
	}
	return nil
}

func (s *session) frame(c *actor.Context, data []byte) {
	var f clientFrame
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	switch f.Op {
	case "view":
		s.view(c, &f)
	case "post":
		if !uuidRe.MatchString(f.ChannelID) {
			return
		}
		req := &pb.PostMessageRequest{ChannelId: f.ChannelID, Content: f.Content, AttachmentIds: f.attachments()}
		s.rpc(c, func(ctx context.Context) any {
			if _, err := s.Client.Messages.PostMessage(ctx, req); err != nil {
				return failed{text: drpc.Err(err).Message}
			}
			return nil
		})
	case "typing":
		if f.ChannelID != s.channelID || !uuidRe.MatchString(f.ChannelID) {
			return
		}
		req := &pb.SendTypingRequest{ChannelId: f.ChannelID}
		s.rpc(c, func(ctx context.Context) any {
			_, _ = s.Client.Messages.SendTyping(ctx, req)
			return nil
		})
	}
}

func (s *session) view(c *actor.Context, f *clientFrame) {
	if !uuidRe.MatchString(f.TeamID) || !slugRe.MatchString(f.TeamName) || !uuidRe.MatchString(f.ChannelID) {
		return
	}
	teamChanged := f.TeamID != s.teamID
	s.teamID, s.teamName, s.channelID = f.TeamID, f.TeamName, f.ChannelID
	s.moderator = f.Moderator == "1"
	s.typingSeq++
	if teamChanged {
		s.loadSidebar(c, false)
	}
	if uuidRe.MatchString(f.LastID) {
		s.markRead(c, f.ChannelID, f.LastID)
	}
	if f.Resync == "1" {
		s.resync(c)
	}
}

func (s *session) markRead(c *actor.Context, channelID, messageID string) {
	s.reads[channelID] = messageID
	if !s.flushing {
		s.flushing = true
		self, engine := c.PID(), c.Engine()
		time.AfterFunc(readDelay, func() { engine.Send(self, flushReads{}) })
	}
}

func (s *session) flush(c *actor.Context) {
	if len(s.reads) == 0 {
		return
	}
	reads := s.reads
	s.reads = make(map[string]string)
	s.rpc(c, func(ctx context.Context) any {
		for ch, id := range reads {
			_, _ = s.Client.Channels.MarkChannelRead(ctx, &pb.MarkChannelReadRequest{ChannelId: ch, MessageId: id})
		}
		return nil
	})
}

func (s *session) event(c *actor.Context, ev *pb.Event) {
	me := s.user.Id
	switch p := ev.Payload.(type) {
	case *pb.Event_MessagePosted:
		m := p.MessagePosted.Message
		mine := m.GetAuthor().GetId() == me
		if m.ChannelId == s.channelID {
			s.fragment("message-new", s.Render.Message(m, me, s.moderator))
			if !mine {
				s.markRead(c, m.ChannelId, m.Id)
			}
			s.typingSeq++
		} else if !mine && s.sidebar[m.ChannelId] {
			s.fragment("unread", m.ChannelId)
		}
	case *pb.Event_MessageEdited:
		m := p.MessageEdited.Message
		if m.ChannelId == s.channelID {
			view := s.Render.Message(m, me, s.moderator)
			view.OOB = true
			s.fragment("message", view)
		}
	case *pb.Event_MessageDeleted:
		if p.MessageDeleted.ChannelId == s.channelID {
			s.fragment("message-deleted", p.MessageDeleted.MessageId)
		}
	case *pb.Event_Typing:
		t := p.Typing
		if t.ChannelId != s.channelID || t.GetUser().GetId() == me {
			return
		}
		s.typingSeq++
		s.fragment("typing", render.Typing{ChannelID: t.ChannelId, Name: render.DisplayName(t.User)})
		seq, ch, self, engine := s.typingSeq, t.ChannelId, c.PID(), c.Engine()
		time.AfterFunc(typingLinger, func() { engine.Send(self, clearTyping{channelID: ch, seq: seq}) })
	case *pb.Event_ChannelJoined:
		if s.teamID != "" {
			s.loadSidebar(c, true)
		}
	case *pb.Event_ChannelLeft:
		if p.ChannelLeft.ChannelId == s.channelID {
			s.toast("error", "You are no longer a member of this channel.")
			s.fragment("ws-control", "/t/"+s.teamName)
			return
		}
		if s.teamID != "" {
			s.loadSidebar(c, true)
		}
	case *pb.Event_TeamJoined:
		if s.teamID != "" {
			s.loadRail(c)
		}
	case *pb.Event_TeamLeft:
		if p.TeamLeft.TeamId == s.teamID {
			s.fragment("ws-control", "/")
			return
		}
		if s.teamID != "" {
			s.loadRail(c)
		}
	case *pb.Event_Presence:
		if s.peers[p.Presence.UserId] {
			s.fragment("presence", render.Presence{ID: p.Presence.UserId, Online: p.Presence.Online})
		}
	}
}
