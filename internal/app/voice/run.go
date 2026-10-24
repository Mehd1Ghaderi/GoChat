// Package voice runs the voice server: browsers talk in voice channels
// through it, with the chat server deciding who may.
package voice

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/client/drpc"
	"github.com/ssenerg/gochat/internal/utils/logging"
	"github.com/ssenerg/gochat/internal/voiceserver"
)

func Run(parent context.Context, cfg *Config) error {
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger, err := logging.New(cfg.Logging)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()
	mainLogger := logger.Named("main")

	if cfg.Voice.ID == "" {
		host, _ := os.Hostname()
		cfg.Voice.ID = sanitizeID(host)
	}

	client, err := drpc.New(cfg.Client.DRPC)
	if err != nil {
		mainLogger.Error("failed to create chat server client", zap.Error(err))
		return err
	}
	defer func() { _ = client.Close() }()

	media, err := voiceserver.NewMedia(cfg.Voice.ICE)
	if err != nil {
		mainLogger.Error("failed to open the media ports", zap.Error(err))
		return err
	}
	defer media.Close()

	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		return err
	}
	server := voiceserver.New(logger, cfg.Voice, client, engine, media)
	defer server.Stop()
	if err := server.Start(); err != nil {
		mainLogger.Error("failed to start http server", zap.Error(err))
		return err
	}

	mainLogger.Info("started", zap.String("id", cfg.Voice.ID), zap.Int("udp_port", cfg.Voice.ICE.UDPPort),
		zap.Int("tcp_port", cfg.Voice.ICE.TCPPort), zap.Strings("public_ips", cfg.Voice.ICE.PublicIPs))
	select {
	case <-ctx.Done():
	case err = <-server.Failures():
		mainLogger.Error("http server failed", zap.Error(err))
	}
	if err != nil {
		return fmt.Errorf("voice server failed: %w", err)
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
		return "voice"
	}
	return string(out)
}
