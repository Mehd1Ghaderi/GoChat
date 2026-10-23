package drpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"
	"storj.io/drpc"
	"storj.io/drpc/drpcerr"
	"storj.io/drpc/drpcmetadata"

	"github.com/ssenerg/gochat/internal/core/models"
	"github.com/ssenerg/gochat/internal/core/secure"
	ierrors "github.com/ssenerg/gochat/internal/errors"
)

// The metadata a gateway sends with every call.
const (
	MetaGatewayKey = "x-gateway-key"
	MetaSession    = "x-session"
	MetaClientIP   = "x-client-ip"
	MetaUserAgent  = "x-user-agent"
)

var streaming = map[string]bool{
	"/gochat.v1.GatewayService/Connect": true,
	"/gochat.v1.FileService/Upload":     true,
	"/gochat.v1.FileService/Download":   true,
}

type principalKey struct{}

type clientKey struct{}

type Client struct {
	IP        string
	UserAgent string
}

// Principal returns the authenticated caller, or Unauthenticated.
func Principal(ctx context.Context) (*models.Principal, error) {
	p, ok := ctx.Value(principalKey{}).(*models.Principal)
	if !ok || p == nil {
		return nil, ierrors.Unauthenticated()
	}
	return p, nil
}

func ClientOf(ctx context.Context) Client {
	c, _ := ctx.Value(clientKey{}).(Client)
	return c
}

// Resolver turns a session token into its principal.
type Resolver func(ctx context.Context, token string) (*models.Principal, error)

type ctxStream struct {
	drpc.Stream
	ctx context.Context
}

func (s *ctxStream) Context() context.Context { return s.ctx }

// Guard authenticates the gateway and the session of every call, bounds unary
// calls in time, and turns every error into what a caller may see.
func Guard(logger *zap.Logger, cfg *Config, resolve Resolver) func(drpc.Handler) drpc.Handler {
	logger = logger.Named("server.drpc")
	return func(next drpc.Handler) drpc.Handler {
		return handlerFunc(func(stream drpc.Stream, rpc string) (err error) {
			start := time.Now()
			defer func() {
				if v := recover(); v != nil {
					logger.Error("rpc panicked", zap.String("rpc", rpc), zap.Any("panic", v), zap.Stack("stack"))
					err = ierrors.Internal(fmt.Errorf("panic: %v", v))
				}
				err = public(logger, rpc, err)
				if logger.Core().Enabled(zap.DebugLevel) {
					logger.Debug("rpc", zap.String("rpc", rpc), zap.Duration("took", time.Since(start)), zap.Error(err))
				}
			}()
			ctx := stream.Context()
			md, _ := drpcmetadata.Get(ctx)
			if !secure.Equal(md[MetaGatewayKey], cfg.GatewayKey) {
				logger.Warn("call without a valid gateway key", zap.String("rpc", rpc))
				return ierrors.Unauthenticated()
			}
			ctx = context.WithValue(ctx, clientKey{}, Client{IP: md[MetaClientIP], UserAgent: md[MetaUserAgent]})
			if token := md[MetaSession]; token != "" {
				p, err := resolve(ctx, token)
				if err != nil && !ierrors.Is(err, ierrors.CodeUnauthenticated) {
					return err
				}
				if p != nil {
					ctx = context.WithValue(ctx, principalKey{}, p)
				}
			}
			if !streaming[rpc] {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, cfg.RequestTimeout)
				defer cancel()
			}
			return next.HandleRPC(&ctxStream{Stream: stream, ctx: ctx}, rpc)
		})
	}
}

type handlerFunc func(stream drpc.Stream, rpc string) error

func (f handlerFunc) HandleRPC(stream drpc.Stream, rpc string) error { return f(stream, rpc) }

// public keeps an error's code and public message and drops its cause.
func public(logger *zap.Logger, rpc string, err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return err
	}
	e := ierrors.As(err)
	if e.Code == ierrors.CodeInternal {
		logger.Error("rpc failed", append(e.Fields(), zap.String("rpc", rpc))...)
	}
	return drpcerr.WithCode(errors.New(e.Message), uint64(e.Code))
}
