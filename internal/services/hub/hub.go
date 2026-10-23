// Package hub routes real-time events to the web gateways serving their
// recipients, and tracks who is online.
package hub

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/config"
)

type Config struct {
	// GatewayQueueLimit is how many events a gateway may lag behind before it
	// is dropped and has to reconnect.
	GatewayQueueLimit int64         `mapstructure:"gateway_queue_limit"`
	PresenceGrace     time.Duration `mapstructure:"presence_grace"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.GatewayQueueLimit < 16 {
		return errors.New("gateway_queue_limit must be at least 16")
	}
	if c.PresenceGrace < 0 {
		return errors.New("presence_grace cannot be negative")
	}
	return nil
}

// Stream is the sending half of a gateway's Connect stream.
type Stream interface {
	Send(*pb.ConnectResponse) error
	Close() error
}

type RegisterGateway struct {
	ID     string
	Stream Stream
}

type UnregisterGateway struct{ ID string }

type Attach struct {
	GatewayID string
	ConnID    uint64
	User      *pb.User
	SessionID string
}

type Reject struct {
	GatewayID string
	ConnID    uint64
	Retry     bool
}

type Detach struct {
	GatewayID string
	ConnID    uint64
}

// Deliver sends Event to the online users among UserIDs. UserIDs must not be
// modified after it is sent.
type Deliver struct {
	UserIDs []string
	Event   *pb.Event
}

type Broadcast struct{ Event *pb.Event }

type RevokeSessions struct {
	UserID     string
	SessionIDs []string
}

// Online replies with the online users among UserIDs.
type Online struct{ UserIDs []string }

type Registered struct{}

type checkOffline struct{ userID string }

// revokedTTL is how long a revoked session stays refused, covering attaches
// authenticated just before the revocation.
const revokedTTL = time.Minute

type conn struct {
	userID    string
	sessionID string
}

type gateway struct {
	id      string
	pid     *actor.PID
	stream  Stream
	pending *atomic.Int64
	conns   map[uint64]conn
}

type hub struct {
	cfg      *Config
	logger   *zap.Logger
	gateways map[string]*gateway
	// online counts each user's connections per gateway.
	online map[string]map[string]int
	// announced holds the users last announced online.
	announced map[string]bool
	// revoked holds recently revoked session ids and when they were revoked.
	revoked map[string]time.Time
}

func New(logger *zap.Logger, cfg *Config) actor.Producer {
	return func() actor.Receiver {
		return &hub{
			cfg:       cfg,
			logger:    logger,
			gateways:  make(map[string]*gateway),
			online:    make(map[string]map[string]int),
			announced: make(map[string]bool),
			revoked:   make(map[string]time.Time),
		}
	}
}

func (h *hub) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case RegisterGateway:
		h.register(c, msg)
	case UnregisterGateway:
		h.unregister(c, msg.ID)
	case Attach:
		h.attach(c, msg)
	case Reject:
		if gw := h.gateways[msg.GatewayID]; gw != nil {
			h.reject(c, gw, msg.ConnID, msg.Retry)
		}
	case Detach:
		h.detach(c, msg)
	case Deliver:
		h.deliver(c, msg)
	case Broadcast:
		resp := &pb.ConnectResponse{Frame: &pb.ConnectResponse_Event{Event: msg.Event}}
		for _, gw := range h.gateways {
			h.send(c, gw, resp)
		}
	case RevokeSessions:
		h.revoke(c, msg)
	case Online:
		online := make([]string, 0, len(msg.UserIDs))
		for _, id := range msg.UserIDs {
			if len(h.online[id]) > 0 {
				online = append(online, id)
			}
		}
		actors.Reply(c, online)
	case checkOffline:
		if len(h.online[msg.userID]) == 0 {
			delete(h.online, msg.userID)
			if h.announced[msg.userID] {
				delete(h.announced, msg.userID)
				h.presence(c, msg.userID, false)
			}
		}
	case actor.Stopped:
		for _, gw := range h.gateways {
			closeStream(gw.stream)
		}
	}
}

// closeStream closes a stream off the actor's goroutine: closing waits for a
// send in progress, which a stalled gateway can hold for long.
func closeStream(s Stream) {
	go func() { _ = s.Close() }()
}

func (h *hub) reject(c *actor.Context, gw *gateway, connID uint64, retry bool) {
	h.send(c, gw, &pb.ConnectResponse{Frame: &pb.ConnectResponse_AttachRejected{AttachRejected: &pb.AttachRejected{ConnId: connID, Retry: retry}}})
}

func (h *hub) register(c *actor.Context, msg RegisterGateway) {
	if old := h.gateways[msg.ID]; old != nil {
		h.unregister(c, msg.ID)
	}
	gw := &gateway{
		id:      msg.ID,
		stream:  msg.Stream,
		pending: new(atomic.Int64),
		conns:   make(map[uint64]conn),
	}
	gw.pid = c.SpawnChild(newWriter(h.logger, gw.id, msg.Stream, gw.pending), "gateway",
		actor.WithMiddleware(actors.Recover(h.logger)))
	h.gateways[msg.ID] = gw
	h.send(c, gw, &pb.ConnectResponse{Frame: &pb.ConnectResponse_Welcome{Welcome: &pb.Welcome{}}})
	h.logger.Info("gateway registered", zap.String("gateway", msg.ID))
	actors.Reply(c, Registered{})
}

func (h *hub) unregister(c *actor.Context, id string) {
	gw := h.gateways[id]
	if gw == nil {
		return
	}
	delete(h.gateways, id)
	for connID := range gw.conns {
		h.removeConn(c, gw, connID)
	}
	c.Engine().Poison(gw.pid)
	h.logger.Info("gateway unregistered", zap.String("gateway", id))
}

func (h *hub) attach(c *actor.Context, msg Attach) {
	gw := h.gateways[msg.GatewayID]
	if gw == nil {
		return
	}
	if _, ok := h.revoked[msg.SessionID]; ok {
		h.reject(c, gw, msg.ConnID, false)
		return
	}
	if _, dup := gw.conns[msg.ConnID]; dup {
		h.removeConn(c, gw, msg.ConnID)
	}
	userID := msg.User.GetId()
	gw.conns[msg.ConnID] = conn{userID: userID, sessionID: msg.SessionID}
	per := h.online[userID]
	if per == nil {
		per = make(map[string]int)
		h.online[userID] = per
	}
	per[gw.id]++
	h.send(c, gw, &pb.ConnectResponse{Frame: &pb.ConnectResponse_Attached{Attached: &pb.Attached{
		ConnId: msg.ConnID, User: msg.User, SessionId: msg.SessionID,
	}}})
	if !h.announced[userID] {
		h.announced[userID] = true
		h.presence(c, userID, true)
	}
}

func (h *hub) detach(c *actor.Context, msg Detach) {
	if gw := h.gateways[msg.GatewayID]; gw != nil {
		h.removeConn(c, gw, msg.ConnID)
	}
}

func (h *hub) removeConn(c *actor.Context, gw *gateway, connID uint64) {
	cn, ok := gw.conns[connID]
	if !ok {
		return
	}
	delete(gw.conns, connID)
	per := h.online[cn.userID]
	if per == nil {
		return
	}
	if per[gw.id]--; per[gw.id] <= 0 {
		delete(per, gw.id)
	}
	if len(per) == 0 {
		// The map stays, empty, until the grace period confirms the user left.
		pid, userID := c.PID(), cn.userID
		engine := c.Engine()
		time.AfterFunc(h.cfg.PresenceGrace, func() { engine.Send(pid, checkOffline{userID: userID}) })
	}
}

func (h *hub) deliver(c *actor.Context, msg Deliver) {
	byGateway := make(map[string][]string)
	for _, uid := range msg.UserIDs {
		for gwID := range h.online[uid] {
			byGateway[gwID] = append(byGateway[gwID], uid)
		}
	}
	for gwID, uids := range byGateway {
		gw := h.gateways[gwID]
		if gw == nil {
			continue
		}
		ev := shallowEvent(msg.Event)
		ev.UserIds = uids
		h.send(c, gw, &pb.ConnectResponse{Frame: &pb.ConnectResponse_Event{Event: ev}})
	}
}

// shallowEvent copies an event's envelope so each gateway gets its own
// recipient list while the payload is shared, read-only.
func shallowEvent(e *pb.Event) *pb.Event {
	out := &pb.Event{Broadcast: e.Broadcast}
	switch p := e.Payload.(type) {
	case *pb.Event_MessagePosted:
		out.Payload = p
	case *pb.Event_MessageEdited:
		out.Payload = p
	case *pb.Event_MessageDeleted:
		out.Payload = p
	case *pb.Event_Typing:
		out.Payload = p
	case *pb.Event_ChannelJoined:
		out.Payload = p
	case *pb.Event_ChannelLeft:
		out.Payload = p
	case *pb.Event_Presence:
		out.Payload = p
	case *pb.Event_TeamJoined:
		out.Payload = p
	case *pb.Event_TeamLeft:
		out.Payload = p
	}
	return out
}

func (h *hub) revoke(c *actor.Context, msg RevokeSessions) {
	if len(msg.SessionIDs) == 0 {
		return
	}
	now := time.Now()
	for id, at := range h.revoked {
		if now.Sub(at) > revokedTTL {
			delete(h.revoked, id)
		}
	}
	for _, id := range msg.SessionIDs {
		h.revoked[id] = now
	}
	resp := &pb.ConnectResponse{Frame: &pb.ConnectResponse_SessionsRevoked{SessionsRevoked: &pb.SessionsRevoked{SessionIds: msg.SessionIDs}}}
	for gwID := range h.online[msg.UserID] {
		gw := h.gateways[gwID]
		if gw == nil {
			continue
		}
		h.send(c, gw, resp)
		for connID, cn := range gw.conns {
			if _, ok := h.revoked[cn.sessionID]; ok && cn.userID == msg.UserID {
				h.removeConn(c, gw, connID)
			}
		}
	}
}

func (h *hub) presence(c *actor.Context, userID string, online bool) {
	ev := &pb.Event{Broadcast: true, Payload: &pb.Event_Presence{Presence: &pb.Presence{UserId: userID, Online: online}}}
	resp := &pb.ConnectResponse{Frame: &pb.ConnectResponse_Event{Event: ev}}
	for _, gw := range h.gateways {
		h.send(c, gw, resp)
	}
}

// send queues resp for the gateway, dropping a gateway that has fallen too far
// behind: it reconnects and its pages resynchronize.
func (h *hub) send(c *actor.Context, gw *gateway, resp *pb.ConnectResponse) {
	if gw.pending.Load() >= h.cfg.GatewayQueueLimit {
		h.logger.Warn("gateway is too slow, dropping it", zap.String("gateway", gw.id))
		closeStream(gw.stream)
		h.unregister(c, gw.id)
		return
	}
	gw.pending.Add(1)
	c.Send(gw.pid, resp)
}

// writer owns the sending half of one gateway's stream.
type writer struct {
	logger  *zap.Logger
	id      string
	stream  Stream
	pending *atomic.Int64
	failed  bool
}

func newWriter(logger *zap.Logger, id string, stream Stream, pending *atomic.Int64) actor.Producer {
	return func() actor.Receiver {
		return &writer{logger: logger, id: id, stream: stream, pending: pending}
	}
}

func (w *writer) Receive(c *actor.Context) {
	resp, ok := c.Message().(*pb.ConnectResponse)
	if !ok {
		return
	}
	w.pending.Add(-1)
	if w.failed {
		return
	}
	if err := w.stream.Send(resp); err != nil {
		w.failed = true
		w.logger.Warn("failed to send to gateway", zap.String("gateway", w.id), zap.Error(err))
		_ = w.stream.Close()
	}
}
