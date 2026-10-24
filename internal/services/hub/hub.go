// Package hub routes real-time events to the web gateways serving their
// recipients, and tracks who is online.
package hub

import (
	"errors"
	"slices"
	"sync/atomic"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	ExpiresAt time.Time
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

// RevokeSessions closes the connections of SessionIDs, or with All every
// connection of UserID.
type RevokeSessions struct {
	UserID     string
	SessionIDs []string
	All        bool
}

// Online replies with the users among UserIDs who are not offline, and
// Statuses with their statuses.
type (
	Online   struct{ UserIDs []string }
	Statuses struct{ UserIDs []string }
)

// SetStatus records the availability a connected user chose.
type SetStatus struct {
	UserID   string
	Status   pb.Status
	DNDUntil time.Time
}

type Registered struct{}

type (
	checkOffline struct{ userID string }
	dndEnded     struct {
		userID string
		until  time.Time
	}
)

type choice struct {
	status   pb.Status
	dndUntil time.Time
}

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
	// announced holds the status last announced of each user not offline,
	// chosen the statuses connected users chose.
	announced map[string]pb.Status
	chosen    map[string]choice
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
			announced: make(map[string]pb.Status),
			chosen:    make(map[string]choice),
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
			if h.status(id) != pb.Status_STATUS_OFFLINE {
				online = append(online, id)
			}
		}
		actors.Reply(c, online)
	case Statuses:
		out := make(map[string]pb.Status, len(msg.UserIDs))
		for _, id := range msg.UserIDs {
			if s := h.status(id); s != pb.Status_STATUS_OFFLINE {
				out[id] = s
			}
		}
		actors.Reply(c, out)
	case SetStatus:
		if len(h.online[msg.UserID]) > 0 {
			h.choose(c, msg.UserID, choice{status: msg.Status, dndUntil: msg.DNDUntil})
			h.announce(c, msg.UserID)
		}
	case dndEnded:
		if ch, ok := h.chosen[msg.userID]; ok && ch.status == pb.Status_STATUS_DND && ch.dndUntil.Equal(msg.until) {
			h.announce(c, msg.userID)
		}
	case checkOffline:
		if len(h.online[msg.userID]) == 0 {
			delete(h.online, msg.userID)
			delete(h.chosen, msg.userID)
			h.announce(c, msg.userID)
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
		ConnId: msg.ConnID, User: msg.User, SessionId: msg.SessionID, ExpiresAt: timestamppb.New(msg.ExpiresAt),
	}}})
	if _, ok := h.chosen[userID]; !ok {
		h.choose(c, userID, choice{status: msg.User.GetStatus(), dndUntil: msg.User.GetDndUntil().AsTime()})
	}
	h.announce(c, userID)
}

func (h *hub) choose(c *actor.Context, userID string, ch choice) {
	if ch.status != pb.Status_STATUS_DND || ch.dndUntil.Unix() <= 0 {
		ch.dndUntil = time.Time{}
	}
	h.chosen[userID] = ch
	if wait := time.Until(ch.dndUntil); wait > 0 {
		pid, engine, ended := c.PID(), c.Engine(), dndEnded{userID: userID, until: ch.dndUntil}
		time.AfterFunc(wait, func() { engine.Send(pid, ended) })
	}
}

// status is how available userID is now.
func (h *hub) status(userID string) pb.Status {
	if len(h.online[userID]) == 0 {
		return pb.Status_STATUS_OFFLINE
	}
	ch := h.chosen[userID]
	switch ch.status {
	case pb.Status_STATUS_AWAY, pb.Status_STATUS_OFFLINE:
		return ch.status
	case pb.Status_STATUS_DND:
		if ch.dndUntil.IsZero() || time.Now().Before(ch.dndUntil) {
			return ch.status
		}
	}
	return pb.Status_STATUS_ONLINE
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
	return &pb.Event{Broadcast: e.Broadcast, Payload: e.Payload}
}

func (h *hub) revoke(c *actor.Context, msg RevokeSessions) {
	if msg.All {
		msg.SessionIDs = slices.Clone(msg.SessionIDs)
		for gwID := range h.online[msg.UserID] {
			gw := h.gateways[gwID]
			if gw == nil {
				continue
			}
			for _, cn := range gw.conns {
				if cn.userID == msg.UserID {
					msg.SessionIDs = append(msg.SessionIDs, cn.sessionID)
				}
			}
		}
		slices.Sort(msg.SessionIDs)
		msg.SessionIDs = slices.Compact(msg.SessionIDs)
	}
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

// announce tells everyone a user's status, when it changed.
func (h *hub) announce(c *actor.Context, userID string) {
	s := h.status(userID)
	if was, ok := h.announced[userID]; (ok && was == s) || (!ok && s == pb.Status_STATUS_OFFLINE) {
		return
	}
	if s == pb.Status_STATUS_OFFLINE {
		delete(h.announced, userID)
	} else {
		h.announced[userID] = s
	}
	ev := &pb.Event{Broadcast: true, Payload: &pb.Event_Presence{Presence: &pb.Presence{
		UserId: userID, Online: s != pb.Status_STATUS_OFFLINE, Status: s,
	}}}
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
