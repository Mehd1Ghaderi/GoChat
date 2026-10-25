// Package gateway is the web gateway's actor system: a router holding the
// event stream to the chat server, and one session actor per websocket.
package gateway

import (
	"context"
	"io"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
)

// Messages the router sends to sessions.
type (
	Ready struct {
		User      *pb.User
		SessionID string
		ExpiresAt time.Time
		// Again is set when the stream reconnected: events may have been lost.
		Again bool
	}
	// Rejected means the session is not valid, TooMany sockets are open, or
	// the attach failed for a passing reason and the page should Retry.
	Rejected struct{ TooMany, Retry bool }
	Revoked  struct{}
	Event    struct{ Event *pb.Event }
)

type register struct {
	connID uint64
	token  string
	pid    *actor.PID
}

type unregister struct{ connID uint64 }

// viewing tells the router the channel a connection shows, empty for none.
type viewing struct {
	connID    uint64
	channelID string
}

// watching tells the router whose presence and profile a connection shows,
// which it tells the server: the server keeps those the user may see.
type watching struct {
	connID uint64
	users  []string
}

type streamUp struct {
	gen    uint64
	stream pb.DRPCGatewayService_ConnectClient
}

type streamDown struct{ gen uint64 }

// streamGen numbers streams across router restarts, so a stale stream's
// messages never match the current one.
var streamGen atomic.Uint64

type inbound struct {
	gen  uint64
	resp *pb.ConnectResponse
}

// attachDue wakes the router when the pace allows the next attach.
type attachDue struct{}

type conn struct {
	pid       *actor.PID
	token     string
	userID    string
	sessionID string
	attached  bool
	// queued is set while the connection waits to attach.
	queued  bool
	viewing string
	watches []string
}

// opener opens a stream to the chat server, and returns how to close it.
type opener func(ctx context.Context) (pb.DRPCGatewayService_ConnectClient, func(), error)

type router struct {
	logger *zap.Logger
	open   opener
	cfg    *Config
	conns  map[uint64]*conn
	byUser map[string]map[uint64]struct{}
	stream pb.DRPCGatewayService_ConnectClient
	gen    uint64
	cancel context.CancelFunc
	// fresh holds new connections and again those to attach after a
	// reconnect; attaches paces both, fresh first.
	fresh, again []uint64
	attaches     *rate.Limiter
	waking       bool
}

func NewRouter(logger *zap.Logger, client *drpc.Client, cfg *Config) actor.Producer {
	return newRouter(logger, dialServer(client), cfg)
}

func newRouter(logger *zap.Logger, open opener, cfg *Config) actor.Producer {
	return func() actor.Receiver {
		return &router{
			logger:   logger,
			open:     open,
			cfg:      cfg,
			conns:    make(map[uint64]*conn),
			byUser:   make(map[string]map[uint64]struct{}),
			attaches: rate.NewLimiter(rate.Limit(cfg.AttachRate), cfg.AttachBurst),
		}
	}
}

func dialServer(client *drpc.Client) opener {
	return func(ctx context.Context) (pb.DRPCGatewayService_ConnectClient, func(), error) {
		cc, err := client.Dial(ctx)
		if err != nil {
			return nil, nil, err
		}
		sctx, cancel := context.WithCancel(client.Stream(ctx, drpc.Call{}))
		stream, err := pb.NewDRPCGatewayServiceClient(cc).Connect(sctx)
		if err != nil {
			cancel()
			_ = cc.Close()
			return nil, nil, err
		}
		return stream, func() { cancel(); _ = cc.Close() }, nil
	}
}

func (r *router) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Started:
		ctx, cancel := context.WithCancel(context.Background())
		r.cancel = cancel
		go r.dial(ctx, c.Engine(), c.PID())
	case actor.Stopped:
		if r.cancel != nil {
			r.cancel()
		}
	case register:
		r.conns[msg.connID] = &conn{pid: msg.pid, token: msg.token, queued: true}
		r.fresh = append(r.fresh, msg.connID)
		r.pace(c)
	case attachDue:
		r.waking = false
		r.pace(c)
	case unregister:
		cn := r.conns[msg.connID]
		if cn == nil {
			return
		}
		r.drop(msg.connID, cn)
		r.write(&pb.ConnectRequest{Frame: &pb.ConnectRequest_Detach{Detach: &pb.Detach{ConnId: msg.connID}}})
	case viewing:
		if cn := r.conns[msg.connID]; cn != nil && cn.viewing != msg.channelID {
			cn.viewing = msg.channelID
			if cn.attached {
				r.view(msg.connID, cn)
			}
		}
	case watching:
		if cn := r.conns[msg.connID]; cn != nil {
			cn.watches = msg.users
			if cn.attached {
				r.watch(msg.connID, cn)
			}
		}
	case streamUp:
		r.gen, r.stream = msg.gen, msg.stream
		r.logger.Info("connected to the chat server")
		for id, cn := range r.conns {
			if !cn.queued {
				cn.queued = true
				r.again = append(r.again, id)
			}
		}
		r.pace(c)
	case streamDown:
		if msg.gen != r.gen {
			return
		}
		r.stream = nil
		r.byUser = make(map[string]map[uint64]struct{})
		r.logger.Warn("lost the chat server, reconnecting")
	case inbound:
		if msg.gen == r.gen {
			r.handle(c, msg.resp)
		}
	}
}

func (r *router) write(req *pb.ConnectRequest) {
	if r.stream == nil {
		return
	}
	if err := r.stream.Send(req); err != nil {
		r.logger.Warn("failed to write to the chat server", zap.Error(err))
		_ = r.stream.Close()
	}
}

// pace attaches the waiting connections as fast as attaches allows, and
// wakes the router for the rest.
func (r *router) pace(c *actor.Context) {
	for r.stream != nil {
		queue := &r.fresh
		if len(*queue) == 0 {
			queue = &r.again
		}
		if len(*queue) == 0 {
			return
		}
		id := (*queue)[0]
		if cn := r.conns[id]; cn != nil && cn.queued {
			res := r.attaches.Reserve()
			if wait := res.Delay(); wait > 0 {
				res.Cancel()
				if !r.waking {
					r.waking = true
					self, engine := c.PID(), c.Engine()
					time.AfterFunc(wait, func() { engine.Send(self, attachDue{}) })
				}
				return
			}
			cn.queued = false
			r.write(&pb.ConnectRequest{Frame: &pb.ConnectRequest_Attach{Attach: &pb.Attach{ConnId: id, SessionToken: cn.token}}})
		}
		if *queue = (*queue)[1:]; len(*queue) == 0 {
			*queue = nil
		}
	}
}

func (r *router) unindex(id uint64, cn *conn) {
	if set := r.byUser[cn.userID]; set != nil {
		delete(set, id)
		if len(set) == 0 {
			delete(r.byUser, cn.userID)
		}
	}
}

// drop forgets a connection.
func (r *router) drop(id uint64, cn *conn) {
	delete(r.conns, id)
	r.unindex(id, cn)
}

func (r *router) view(id uint64, cn *conn) {
	r.write(&pb.ConnectRequest{Frame: &pb.ConnectRequest_View{View: &pb.View{ConnId: id, ChannelId: cn.viewing}}})
}

// watch tells the server whose presence and profile a connection shows.
func (r *router) watch(id uint64, cn *conn) {
	r.write(&pb.ConnectRequest{Frame: &pb.ConnectRequest_Watch{Watch: &pb.Watch{ConnId: id, UserIds: cn.watches}}})
}

func (r *router) handle(c *actor.Context, resp *pb.ConnectResponse) {
	switch f := resp.Frame.(type) {
	case *pb.ConnectResponse_Attached:
		id := f.Attached.ConnId
		cn := r.conns[id]
		if cn == nil {
			r.write(&pb.ConnectRequest{Frame: &pb.ConnectRequest_Detach{Detach: &pb.Detach{ConnId: id}}})
			return
		}
		userID := f.Attached.User.GetId()
		if !cn.attached && len(r.byUser[userID]) >= r.cfg.MaxPerUser {
			r.drop(id, cn)
			r.write(&pb.ConnectRequest{Frame: &pb.ConnectRequest_Detach{Detach: &pb.Detach{ConnId: id}}})
			c.Send(cn.pid, Rejected{TooMany: true})
			return
		}
		again := cn.attached
		cn.attached = true
		cn.userID = userID
		cn.sessionID = f.Attached.SessionId
		set := r.byUser[cn.userID]
		if set == nil {
			set = make(map[uint64]struct{})
			r.byUser[cn.userID] = set
		}
		set[id] = struct{}{}
		if cn.viewing != "" {
			r.view(id, cn)
		}
		if len(cn.watches) > 0 {
			r.watch(id, cn)
		}
		c.Send(cn.pid, Ready{User: f.Attached.User, SessionID: cn.sessionID, ExpiresAt: f.Attached.GetExpiresAt().AsTime(), Again: again})
	case *pb.ConnectResponse_AttachRejected:
		if cn := r.conns[f.AttachRejected.ConnId]; cn != nil {
			r.drop(f.AttachRejected.ConnId, cn)
			c.Send(cn.pid, Rejected{Retry: f.AttachRejected.Retry})
		}
	case *pb.ConnectResponse_Event:
		ev := Event{Event: f.Event}
		if !f.Event.Broadcast {
			for _, id := range f.Event.ConnIds {
				if cn := r.conns[id]; cn != nil && cn.attached {
					c.Send(cn.pid, ev)
				}
			}
			return
		}
		for _, cn := range r.conns {
			if cn.attached {
				c.Send(cn.pid, ev)
			}
		}
	case *pb.ConnectResponse_SessionsRevoked:
		revoked := make(map[string]bool, len(f.SessionsRevoked.SessionIds))
		for _, id := range f.SessionsRevoked.SessionIds {
			revoked[id] = true
		}
		for id, cn := range r.conns {
			if revoked[cn.sessionID] {
				r.drop(id, cn)
				r.write(&pb.ConnectRequest{Frame: &pb.ConnectRequest_Detach{Detach: &pb.Detach{ConnId: id}}})
				c.Send(cn.pid, Revoked{})
			}
		}
	}
}

// dial keeps one stream to the server open, backing off between attempts.
func (r *router) dial(ctx context.Context, engine *actor.Engine, self *actor.PID) {
	delay := r.cfg.ReconnectMin
	for ctx.Err() == nil {
		gen := streamGen.Add(1)
		started := time.Now()
		err := r.session(ctx, engine, self, gen)
		engine.Send(self, streamDown{gen: gen})
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > time.Minute {
			delay = r.cfg.ReconnectMin
		}
		r.logger.Warn("chat server stream ended", zap.Error(err), zap.Duration("retry_in", delay))
		jitter := time.Duration(rand.Int64N(int64(delay)/2 + 1)) //nolint:gosec // jitter needs no secrecy
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay/2 + jitter):
		}
		delay = min(delay*2, r.cfg.ReconnectMax)
	}
}

func (r *router) session(ctx context.Context, engine *actor.Engine, self *actor.PID, gen uint64) error {
	stream, done, err := r.open(ctx)
	if err != nil {
		return err
	}
	defer done()
	if err := stream.Send(&pb.ConnectRequest{Frame: &pb.ConnectRequest_Hello{Hello: &pb.Hello{GatewayId: r.cfg.ID}}}); err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.GetWelcome() == nil {
		return io.ErrUnexpectedEOF
	}
	engine.Send(self, streamUp{gen: gen, stream: stream})
	for {
		resp, err := stream.Recv()
		if err != nil {
			return err
		}
		engine.Send(self, inbound{gen: gen, resp: resp})
	}
}
