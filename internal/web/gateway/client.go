package gateway

import (
	"context"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/coder/websocket"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
)

// The close codes of the client API's event socket; see client.proto.
const (
	CloseRetry           websocket.StatusCode = 4000
	CloseUnauthenticated websocket.StatusCode = 4001
	CloseRevoked         websocket.StatusCode = 4003
	CloseTooMany         websocket.StatusCode = 4008
	CloseTooSlow         websocket.StatusCode = 4009
)

// clientSession serves one event socket of the client API: native apps get
// the person's events as the chat server sends them, in binary ClientFrames,
// and render them themselves.
type clientSession struct {
	*Deps
	connID   uint64
	call     drpc.Call
	ws       *websocket.Conn
	out      chan outMsg
	closing  bool
	stopPing context.CancelFunc
	expiry   *time.Timer
}

func NewClientSession(d *Deps, connID uint64, call drpc.Call, ws *websocket.Conn) actor.Producer {
	return func() actor.Receiver {
		return &clientSession{Deps: d, connID: connID, call: call, ws: ws, out: make(chan outMsg, d.Cfg.SendQueue)}
	}
}

func (s *clientSession) Receive(c *actor.Context) {
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
		if s.expiry != nil {
			s.expiry.Stop()
		}
		if !s.closing {
			s.closing = true
			close(s.out)
		}
	case Ready:
		// The socket ends with its session.
		if s.expiry == nil && !msg.ExpiresAt.IsZero() {
			pid, engine := c.PID(), c.Engine()
			s.expiry = time.AfterFunc(time.Until(msg.ExpiresAt), func() { engine.Send(pid, expired{}) })
		}
		ready := &pb.ClientReady{User: msg.User, SessionId: msg.SessionID, Resync: msg.Again}
		if !msg.ExpiresAt.IsZero() {
			ready.ExpiresAt = timestamppb.New(msg.ExpiresAt)
		}
		s.send(&pb.ClientFrame{Frame: &pb.ClientFrame_Ready{Ready: ready}})
	case Rejected:
		switch {
		case msg.TooMany:
			s.close(CloseTooMany)
		case msg.Retry:
			s.close(CloseRetry)
		default:
			s.close(CloseUnauthenticated)
		}
	case Revoked, expired:
		s.close(CloseRevoked)
	case Event:
		// The router shares one event among its recipients, and its
		// recipient list names the others on this gateway: the app gets the
		// payload alone.
		s.send(&pb.ClientFrame{Frame: &pb.ClientFrame_Event{Event: &pb.Event{Payload: msg.Event.Payload}}})
	}
}

// send queues a frame, closing a socket whose app does not keep up: it
// reconnects and loads what it shows again.
func (s *clientSession) send(f *pb.ClientFrame) {
	if s.closing {
		return
	}
	data, err := proto.Marshal(f)
	if err != nil {
		s.Logger.Error("failed to encode a client frame", zap.Error(err))
		return
	}
	select {
	case s.out <- outMsg{data: data}:
	default:
		s.Logger.Debug("app too slow, closing its socket", zap.Uint64("conn", s.connID))
		s.closing = true
		close(s.out)
		// The queue is full: the close goes out of line, as the handshake
		// may take a while.
		go func() { _ = s.ws.Close(CloseTooSlow, "too far behind") }()
	}
}

func (s *clientSession) close(code websocket.StatusCode) {
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

func (s *clientSession) write() {
	for m := range s.out {
		if m.close {
			_ = s.ws.Close(m.code, "")
			s.drain()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.Cfg.WriteTimeout)
		err := s.ws.Write(ctx, websocket.MessageBinary, m.data)
		cancel()
		if err != nil {
			_ = s.ws.CloseNow()
			s.drain()
			return
		}
	}
	_ = s.ws.Close(websocket.StatusNormalClosure, "")
}

func (s *clientSession) drain() {
	for range s.out {
	}
}

func (s *clientSession) ping(ctx context.Context) {
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
