// Package web runs the web gateway: htmx pages for browsers, backed by the
// chat server over DRPC, with one session actor per open page.
package web

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/client/drpc"
	"github.com/ssenerg/gochat/internal/utils/logging"
	"github.com/ssenerg/gochat/internal/web"
	"github.com/ssenerg/gochat/internal/web/gateway"
	"github.com/ssenerg/gochat/internal/web/render"
)

const actorsStopTimeout = 10 * time.Second

func Run(parent context.Context, cfg *Config) error {
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger, err := logging.New(cfg.Logging)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()
	mainLogger := logger.Named("main")

	if cfg.Gateway.ID == "" {
		host, _ := os.Hostname()
		cfg.Gateway.ID = sanitizeID(host)
	}

	client, err := drpc.New(cfg.Client.DRPC)
	if err != nil {
		mainLogger.Error("failed to create chat server client", zap.Error(err))
		return err
	}
	defer func() { _ = client.Close() }()

	renderer, err := render.New(cfg.Web.RenderCacheSize)
	if err != nil {
		mainLogger.Error("failed to load templates", zap.Error(err))
		return err
	}

	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		return err
	}
	events := actors.LogEvents(engine, logger.Named("actors"))
	router := engine.Spawn(gateway.NewRouter(logger.Named("router"), client, cfg.Gateway), "router", actor.WithID("main"),
		actor.WithMiddleware(actors.Recover(logger)))
	deps := &gateway.Deps{Logger: logger.Named("session"), Cfg: cfg.Gateway, Client: client, Render: renderer, Router: router}

	handler := web.NewHandler(logger, cfg.Web, client, renderer, engine, deps)
	server := web.NewServer(logger, cfg.Web.HTTP, handler.Routes())
	defer func() {
		server.Stop()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), actorsStopTimeout)
		defer stopCancel()
		<-engine.PoisonCtx(stopCtx, router).Done()
		<-engine.PoisonCtx(stopCtx, events).Done()
	}()
	if err := server.Start(); err != nil {
		mainLogger.Error("failed to start http server", zap.Error(err))
		return err
	}

	mainLogger.Info("started", zap.String("public_url", cfg.Web.PublicURL), zap.String("gateway", cfg.Gateway.ID))
	select {
	case <-ctx.Done():
	case err = <-server.Failures():
		mainLogger.Error("http server failed", zap.Error(err))
	}
	if err != nil {
		return fmt.Errorf("web gateway failed: %w", err)
	}
	return nil
}

func sanitizeID(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(out) < 64; i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "gateway"
	}
	return string(out)
}
