// Package gateway is the web gateway's actor system: a router holding the
// event stream to the chat server, and one session actor per websocket.
package gateway

import (
	"context"
	"io"
	"math/rand/v2"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
)

// Messages the router sends to sessions.
type (
	Ready struct {
		User      *pb.User
		SessionID string
		// Again is set when the stream reconnected: events may have been lost.
		Again bool
	}
	// Rejected means the session is not valid, or TooMany sockets are open.
	Rejected struct{ TooMany bool }
	Revoked  struct{}
	Event    struct{ Event *pb.Event }
)

type register struct {
	connID uint64
	token  string
	pid    *actor.PID
}

type unregister struct{ connID uint64 }

type streamUp struct {
	gen    uint64
	stream pb.DRPCGatewayService_ConnectClient
}

type streamDown struct{ gen uint64 }

type inbound struct {
	gen  uint64
	resp *pb.ConnectResponse
}

type conn struct {
	pid       *actor.PID
	token     string
	userID    string
	sessionID string
	attached  bool
}

type router struct {
	logger *zap.Logger
	client *drpc.Client
	cfg    *Config
	conns  map[uint64]*conn
	byUser map[string]map[uint64]struct{}
	stream pb.DRPCGatewayService_ConnectClient
	gen    uint64
	cancel context.CancelFunc
}

func NewRouter(logger *zap.Logger, client *drpc.Client, cfg *Config) actor.Producer {
	return func() actor.Receiver {
		return &router{
			logger: logger,
			client: client,
			cfg:    cfg,
			conns:  make(map[uint64]*conn),
			byUser: make(map[string]map[uint64]struct{}),
		}
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
		r.conns[msg.connID] = &conn{pid: msg.pid, token: msg.token}
		r.attach(msg.connID, msg.token)
	case unregister:
		cn := r.conns[msg.connID]
		if cn == nil {
			return
		}
		delete(r.conns, msg.connID)
		r.unindex(msg.connID, cn)
		r.write(&pb.ConnectRequest{Frame: &pb.ConnectRequest_Detach{Detach: &pb.Detach{ConnId: msg.connID}}})
	case streamUp:
		r.gen, r.stream = msg.gen, msg.stream
		r.logger.Info("connected to the chat server")
		for id, cn := range r.conns {
			r.attach(id, cn.token)
		}
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

func (r *router) attach(id uint64, token string) {
	r.write(&pb.ConnectRequest{Frame: &pb.ConnectRequest_Attach{Attach: &pb.Attach{ConnId: id, SessionToken: token}}})
}

func (r *router) unindex(id uint64, cn *conn) {
	if set := r.byUser[cn.userID]; set != nil {
		delete(set, id)
		if len(set) == 0 {
			delete(r.byUser, cn.userID)
		}
	}
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
			delete(r.conns, id)
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
		c.Send(cn.pid, Ready{User: f.Attached.User, SessionID: cn.sessionID, Again: again})
	case *pb.ConnectResponse_AttachRejected:
		if cn := r.conns[f.AttachRejected.ConnId]; cn != nil {
			delete(r.conns, f.AttachRejected.ConnId)
			r.unindex(f.AttachRejected.ConnId, cn)
			c.Send(cn.pid, Rejected{})
		}
	case *pb.ConnectResponse_Event:
		ev := Event{Event: f.Event}
		if f.Event.Broadcast {
			for _, cn := range r.conns {
				if cn.attached {
					c.Send(cn.pid, ev)
				}
			}
			return
		}
		for _, uid := range f.Event.UserIds {
			for id := range r.byUser[uid] {
				c.Send(r.conns[id].pid, ev)
			}
		}
	case *pb.ConnectResponse_SessionsRevoked:
		revoked := make(map[string]bool, len(f.SessionsRevoked.SessionIds))
		for _, id := range f.SessionsRevoked.SessionIds {
			revoked[id] = true
		}
		for id, cn := range r.conns {
			if revoked[cn.sessionID] {
				delete(r.conns, id)
				r.unindex(id, cn)
				c.Send(cn.pid, Revoked{})
			}
		}
	}
}

// dial keeps one stream to the server open, backing off between attempts.
func (r *router) dial(ctx context.Context, engine *actor.Engine, self *actor.PID) {
	var gen uint64
	delay := r.cfg.ReconnectMin
	for ctx.Err() == nil {
		gen++
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
	cc, err := r.client.Dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = cc.Close() }()
	sctx, cancel := context.WithCancel(r.client.Stream(ctx, drpc.Call{}))
	defer cancel()
	stream, err := pb.NewDRPCGatewayServiceClient(cc).Connect(sctx)
	if err != nil {
		return err
	}
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
