package gateway

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/coder/websocket"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/proto"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
	"github.com/ssenerg/gochat/internal/web/render"
)

const (
	typingLinger = 6 * time.Second
	readDelay    = time.Second
	// leaveGrace lets the tab that left a channel navigate on its own.
	leaveGrace = 2 * time.Second
	// earlyFrames and queuedPosts bound what a browser may send ahead.
	earlyFrames = 32
	queuedPosts = 32
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
	flushThread  struct{}
	sidebarReady struct {
		teamID string
		lists  *render.Lists
	}
	railReady  struct{ rail *render.Rail }
	mutedReady struct{ ids []string }
	savedReady struct{ ids []string }
	// refetched is a message read again to show it as it now is.
	refetched struct{ m *pb.Message }
	failed    struct{ text string }
	expired   struct{}
	// viewChecked carries the newest message of the channel a tab opened.
	viewChecked struct{ channelID, last string }
	leftChannel struct{ channelID string }
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
	// anchored is set while the tab shows an older part of its channel,
	// which takes no new messages.
	anchored bool
	sidebar  map[string]bool
	// mentions counts unread mentions per channel in the sidebar.
	mentions map[string]int
	peers    map[string]bool
	directs  map[string]bool
	// muted holds the channels the user muted, names the sidebar's titles.
	muted map[string]bool
	names map[string]string
	// saved holds the messages the user saved.
	saved     map[string]bool
	typingSeq uint64
	// typer is who the typing line names, and shown the newest message on
	// the page.
	typer string
	shown string
	// threadID is the root of the thread the tab has open, in threadChannel.
	threadID      string
	threadChannel string
	threadRead    bool
	// inThreads is set while the tab shows the threads it follows.
	inThreads bool
	resynced  string
	posts     chan *pb.PostMessageRequest
	reads     map[string]string
	flushing  bool
	closing   bool
	stopPing  context.CancelFunc
	expiry    *time.Timer
	// early holds what the browser sent before the server attached it.
	early [][]byte
}

func NewSession(d *Deps, connID uint64, call drpc.Call, ws *websocket.Conn) actor.Producer {
	return func() actor.Receiver {
		return &session{
			Deps:     d,
			connID:   connID,
			call:     call,
			ws:       ws,
			out:      make(chan outMsg, d.Cfg.SendQueue),
			limiter:  rate.NewLimiter(rate.Limit(d.Cfg.FrameRate), d.Cfg.FrameBurst),
			sidebar:  make(map[string]bool),
			mentions: make(map[string]int),
			muted:    make(map[string]bool),
			saved:    make(map[string]bool),
			names:    make(map[string]string),
			peers:    make(map[string]bool),
			reads:    make(map[string]string),
			posts:    make(chan *pb.PostMessageRequest, queuedPosts),
		}
	}
}

func (s *session) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Started:
		go s.write()
		go s.post(c.PID(), c.Engine())
		ctx, cancel := context.WithCancel(context.Background())
		s.stopPing = cancel
		go s.ping(ctx)
		c.Send(s.Router, register{connID: s.connID, token: s.call.Session, pid: c.PID()})
	case actor.Stopped:
		s.flush(c)
		close(s.posts)
		c.Send(s.Router, unregister{connID: s.connID})
		s.stopPing()
		if s.expiry != nil {
			s.expiry.Stop()
		}
		if !s.closing {
			s.closing = true
			close(s.out)
		}
	case Ready:
		s.user, s.sessionID = msg.User, msg.SessionID
		// The socket ends with its session.
		if s.expiry == nil && !msg.ExpiresAt.IsZero() {
			pid, engine := c.PID(), c.Engine()
			s.expiry = time.AfterFunc(time.Until(msg.ExpiresAt), func() { engine.Send(pid, expired{}) })
		}
		for _, data := range s.early {
			s.frame(c, data)
		}
		s.early = nil
		if msg.Again {
			s.resync(c)
		}
		s.rpc(c, func(ctx context.Context) any {
			res, err := s.Client.Channels.ListMutedChannels(ctx, &pb.ListMutedChannelsRequest{})
			if err != nil {
				return nil
			}
			return mutedReady{ids: res.ChannelIds}
		})
		s.rpc(c, func(ctx context.Context) any {
			res, err := s.Client.Messages.ListSaved(ctx, &pb.ListSavedRequest{IdsOnly: true})
			if err != nil {
				return nil
			}
			return savedReady{ids: res.MessageIds}
		})
	case Rejected:
		if msg.Retry {
			s.close(websocket.StatusTryAgainLater)
			return
		}
		if msg.TooMany {
			s.toast("error", "Too many open tabs: close one and reload.")
			s.close(websocket.StatusPolicyViolation)
			return
		}
		s.redirect("/login")
	case Revoked, expired:
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
		} else if len(s.early) < earlyFrames {
			s.early = append(s.early, msg.Data)
		} else {
			s.toast("error", "Some messages could not be sent: reload and try again.")
		}
	case clearTyping:
		if msg.seq == s.typingSeq && msg.channelID == s.channelID {
			s.clearTyping()
		}
	case viewChecked:
		// A message posted while the page loaded reached neither the page
		// nor this socket: reload the channel, once.
		if msg.channelID == s.channelID && !s.anchored && msg.last != "" && render.Newer(msg.last, s.shown) && s.resynced != msg.channelID {
			s.resynced = msg.channelID
			s.fragment("resync", s.channelURL())
		}
	case leftChannel:
		if msg.channelID == s.channelID {
			s.fragment("ws-control", "/t/"+s.teamName)
		}
	case flushReads:
		s.flushing = false
		s.flush(c)
	case flushThread:
		s.threadRead = false
		if s.threadID != "" {
			req := &pb.MarkThreadReadRequest{ChannelId: s.threadChannel, RootId: s.threadID}
			s.rpc(c, func(ctx context.Context) any {
				_, _ = s.Client.Messages.MarkThreadRead(ctx, req)
				return nil
			})
		}
	case sidebarReady:
		if msg.teamID != s.teamID {
			return
		}
		s.sidebar = make(map[string]bool, len(msg.lists.Channels)+len(msg.lists.DMs))
		s.mentions = make(map[string]int)
		s.peers = make(map[string]bool, len(msg.lists.DMs))
		s.directs = make(map[string]bool, len(msg.lists.DMs))
		for _, ch := range msg.lists.Channels {
			s.sidebar[ch.Id] = true
			s.mentions[ch.Id] = int(ch.MentionCount)
			s.names[ch.Id] = render.Title(ch)
			s.muted[ch.Id] = ch.Muted
		}
		for _, ch := range msg.lists.DMs {
			s.sidebar[ch.Id] = true
			s.directs[ch.Id] = true
			if ch.Type == pb.ChannelType_CHANNEL_TYPE_GROUP {
				s.names[ch.Id] = render.Title(ch)
			}
			s.mentions[ch.Id] = int(ch.MentionCount)
			s.muted[ch.Id] = ch.Muted
			if ch.Peer != nil {
				s.peers[ch.Peer.Id] = true
			}
		}
		if msg.lists.OOB {
			msg.lists.Active = s.channelID
			if s.inThreads {
				msg.lists.Active = "threads"
			}
			s.fragment("channel-lists", msg.lists)
		}
	case railReady:
		s.fragment("team-rail", msg.rail)
	case mutedReady:
		for _, id := range msg.ids {
			s.muted[id] = true
		}
	case savedReady:
		for _, id := range msg.ids {
			s.saved[id] = true
		}
	case refetched:
		if s.inView(msg.m.ChannelId, msg.m.RootId) {
			view := s.show(msg.m)
			view.OOB = true
			s.message("message", view)
		}
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

func (s *session) message(name string, m *render.Message) {
	b, err := s.Render.MessageBytes(name, m)
	if err != nil {
		s.Logger.Error("failed to render a message", zap.String("fragment", name), zap.Error(err))
		return
	}
	s.send(b)
}

// show prepares m for this tab, as its reader saved it.
func (s *session) show(m *pb.Message) *render.Message {
	view := s.Render.Message(m, s.user, s.moderator)
	view.Saved = s.saved[m.Id]
	return view
}

// inView reports whether the tab shows the message of channelID, a reply
// when rootID is set.
func (s *session) inView(channelID, rootID string) bool {
	if rootID == "" {
		return channelID == s.channelID
	}
	return rootID == s.threadID
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
		var unread int
		if t, err := s.Client.Messages.ListThreads(ctx, &pb.ListThreadsRequest{TeamId: teamID}); err == nil {
			unread = int(t.UnreadCount)
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
		return sidebarReady{teamID: teamID, lists: &render.Lists{Team: team, Channels: channels, DMs: direct, Online: online, ThreadsUnread: unread, OOB: oob}}
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
	Anchored   string          `json:"anchored"`
	RootID     string          `json:"root_id"`
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
		if f.RootID != "" && !uuidRe.MatchString(f.RootID) {
			return
		}
		select {
		case s.posts <- &pb.PostMessageRequest{ChannelId: f.ChannelID, Content: f.Content, AttachmentIds: f.attachments(), RootId: f.RootID}:
		default:
			s.toast("error", "You are sending too fast: a message was not sent.")
		}
	case "thread":
		if (f.RootID == "" || uuidRe.MatchString(f.RootID)) && (f.ChannelID == "" || uuidRe.MatchString(f.ChannelID)) {
			s.threadID, s.threadChannel = f.RootID, f.ChannelID
		}
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

// post sends a tab's messages one at a time, in the order it wrote them.
func (s *session) post(self *actor.PID, engine *actor.Engine) {
	for req := range s.posts {
		ctx, cancel := s.Client.Context(context.Background(), s.call)
		_, err := s.Client.Messages.PostMessage(ctx, req)
		cancel()
		if err != nil {
			engine.Send(self, failed{text: drpc.Err(err).Message})
		}
	}
}

// view follows what the tab shows: a channel, or with no channel the
// threads it follows.
func (s *session) view(c *actor.Context, f *clientFrame) {
	if !uuidRe.MatchString(f.TeamID) || !slugRe.MatchString(f.TeamName) || (f.ChannelID != "" && !uuidRe.MatchString(f.ChannelID)) {
		return
	}
	teamChanged := f.TeamID != s.teamID
	if f.ChannelID != s.channelID {
		s.resynced, s.threadID = "", ""
	}
	s.teamID, s.teamName, s.channelID = f.TeamID, f.TeamName, f.ChannelID
	s.inThreads = f.ChannelID == ""
	s.moderator = f.Moderator == "1"
	s.anchored = f.Anchored == "1"
	s.typingSeq++
	s.typer, s.shown = "", ""
	if teamChanged {
		s.loadSidebar(c, false)
	}
	if s.inThreads {
		return
	}
	s.mentions[f.ChannelID] = 0
	if uuidRe.MatchString(f.LastID) {
		s.shown = f.LastID
		s.markRead(c, f.ChannelID, f.LastID)
	}
	if f.Resync == "1" {
		s.resync(c)
		return
	}
	chID := f.ChannelID
	s.rpc(c, func(ctx context.Context) any {
		res, err := s.Client.Channels.GetChannel(ctx, &pb.GetChannelRequest{ChannelId: chID})
		if err != nil {
			return nil
		}
		return viewChecked{channelID: chID, last: res.Channel.GetLastMessageId()}
	})
}

// notify asks the tab to show a desktop notification for a message to the
// user, in a direct conversation, by mention or in a thread they take part
// in. The tab shows it when it is in the background or elsewhere.
func (s *session) notify(m *pb.Message, participants []string) {
	if s.muted[m.ChannelId] {
		return
	}
	direct := s.directs[m.ChannelId]
	reply := m.RootId != "" && slices.Contains(participants, s.user.Id)
	if !direct && !reply && !render.Mentions(m, s.user) {
		return
	}
	n := render.Notice{Title: render.DisplayName(m.Author), Body: render.Excerpt(m), URL: "/c/" + m.ChannelId, Tag: m.Id}
	switch {
	case m.RootId != "":
		n.Title += " replied in a thread"
		n.URL += "?thread=" + m.RootId
	case s.names[m.ChannelId] != "":
		n.Title += " in " + s.names[m.ChannelId]
	}
	s.fragment("notify", map[string]any{"Notice": n, "Channel": m.ChannelId, "Root": m.RootId})
}

// mentioned counts one more unread mention in a sidebar channel.
func (s *session) mentioned(channelID string) {
	if !s.sidebar[channelID] {
		return
	}
	s.mentions[channelID]++
	s.fragment("badge", map[string]any{"ID": channelID, "Count": s.mentions[channelID], "OOB": true})
}

// direct reports whether a sidebar channel is a direct conversation.
func (s *session) direct(channelID string) bool {
	return s.directs[channelID]
}

// readThread marks the open thread read a moment after a reply showed in it.
func (s *session) readThread(c *actor.Context) {
	if !s.threadRead {
		s.threadRead = true
		self, engine := c.PID(), c.Engine()
		time.AfterFunc(readDelay, func() { engine.Send(self, flushThread{}) })
	}
}

func (s *session) clearTyping() {
	if s.typer != "" {
		s.typer = ""
		s.fragment("typing", render.Typing{ChannelID: s.channelID})
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
		if !mine {
			s.notify(m, p.MessagePosted.ParticipantIds)
		}
		if m.RootId != "" {
			if m.RootId == s.threadID {
				s.message("reply-new", s.show(m))
				if !mine {
					s.readThread(c)
				}
			} else if !mine && m.ChannelId != s.channelID && render.Mentions(m, s.user) {
				s.mentioned(m.ChannelId)
			}
			if !mine && m.RootId != s.threadID && slices.Contains(p.MessagePosted.ParticipantIds, me) {
				s.fragment("threads-unread", true)
				if s.inThreads {
					s.fragment("threads-refresh", s.teamName)
				}
			}
			return
		}
		if m.ChannelId == s.channelID {
			if s.anchored {
				return
			}
			s.message("message-new", s.show(m))
			if render.Newer(m.Id, s.shown) {
				s.shown = m.Id
			}
			if !mine {
				s.markRead(c, m.ChannelId, m.Id)
			}
			if m.GetAuthor().GetId() == s.typer {
				s.typingSeq++
				s.clearTyping()
			}
		} else if !mine && s.sidebar[m.ChannelId] {
			if s.direct(m.ChannelId) || render.Mentions(m, s.user) {
				s.mentioned(m.ChannelId)
			} else if s.mentions[m.ChannelId] == 0 && !s.muted[m.ChannelId] {
				s.fragment("unread", m.ChannelId)
			}
		}
	case *pb.Event_MessageEdited:
		m := p.MessageEdited.Message
		if s.inView(m.ChannelId, m.RootId) {
			view := s.show(m)
			view.OOB = true
			s.message("message", view)
		}
	case *pb.Event_MessageDeleted:
		d := p.MessageDeleted
		if d.MessageId == s.threadID {
			s.threadID = ""
			s.fragment("thread-closed", nil)
		}
		if s.inView(d.ChannelId, d.RootId) {
			s.fragment("message-deleted", d.MessageId)
		}
	case *pb.Event_ChannelUpdated:
		// The payload is shared by every socket: read only.
		ch := proto.CloneOf(p.ChannelUpdated.Channel)
		if s.sidebar[ch.Id] || (ch.ArchivedAt == nil && ch.TeamId == s.teamID) {
			s.loadSidebar(c, true)
		}
		if ch.Id == s.channelID {
			ch.Muted = s.muted[ch.Id]
			view := &render.ChannelView{
				Team: &pb.Team{Id: s.teamID, Name: s.teamName}, Channel: ch, Title: render.Title(ch),
				Member: true, Moderator: s.moderator, Archived: ch.ArchivedAt != nil, OOB: true,
			}
			s.fragment("channel-header", view)
			s.fragment("composer-area", view)
		}
	case *pb.Event_SavedChanged:
		d := p.SavedChanged
		if d.Saved {
			s.saved[d.MessageId] = true
		} else {
			delete(s.saved, d.MessageId)
		}
		s.fragment("saved-changed", nil)
		if s.inView(d.ChannelId, d.RootId) {
			req := &pb.GetMessageRequest{ChannelId: d.ChannelId, MessageId: d.MessageId, RootId: d.RootId}
			s.rpc(c, func(ctx context.Context) any {
				res, err := s.Client.Messages.GetMessage(ctx, req)
				if err != nil {
					return nil
				}
				return refetched{m: res.Message}
			})
		}
	case *pb.Event_PinsChanged:
		if d := p.PinsChanged; d.ChannelId == s.channelID {
			s.fragment("pins-button", map[string]any{"Team": s.teamName, "Channel": d.ChannelId, "Count": d.Count, "OOB": true})
		}
	case *pb.Event_ChannelMuted:
		s.muted[p.ChannelMuted.ChannelId] = p.ChannelMuted.Muted
		if s.sidebar[p.ChannelMuted.ChannelId] {
			s.loadSidebar(c, true)
		}
	case *pb.Event_ReactionsChanged:
		d := p.ReactionsChanged
		if s.inView(d.ChannelId, d.RootId) {
			s.fragment("reactions", render.Reactions(d.ChannelId, d.MessageId, d.RootId, d.Reactions, s.user, true))
		}
	case *pb.Event_Typing:
		t := p.Typing
		if t.ChannelId != s.channelID || t.GetUser().GetId() == me {
			return
		}
		s.typingSeq++
		s.typer = t.GetUser().GetId()
		s.fragment("typing", render.Typing{ChannelID: t.ChannelId, Name: render.DisplayName(t.User)})
		seq, ch, self, engine := s.typingSeq, t.ChannelId, c.PID(), c.Engine()
		time.AfterFunc(typingLinger, func() { engine.Send(self, clearTyping{channelID: ch, seq: seq}) })
	case *pb.Event_ChannelJoined:
		if s.teamID != "" {
			s.loadSidebar(c, true)
		}
	case *pb.Event_ChannelLeft:
		if p.ChannelLeft.ChannelId == s.channelID {
			if !p.ChannelLeft.Own {
				s.toast("error", "You are no longer a member of this channel.")
				s.fragment("ws-control", "/t/"+s.teamName)
				return
			}
			// The tab that left moves on by itself; others showing the
			// channel follow.
			msg, self, engine := leftChannel{channelID: s.channelID}, c.PID(), c.Engine()
			time.AfterFunc(leaveGrace, func() { engine.Send(self, msg) })
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
