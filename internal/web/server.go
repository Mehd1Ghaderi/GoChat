// Package web serves the browser: htmx pages and fragments over HTTP, and the
// websocket each page holds for its real-time events.
package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"

	"go.uber.org/zap"
)

type Server struct {
	logger   *zap.Logger
	cfg      *HTTPConfig
	srv      *http.Server
	failures chan error
	started  bool
	stopOnce sync.Once
}

func NewServer(logger *zap.Logger, cfg *HTTPConfig, handler http.Handler) *Server {
	s := &Server{logger: logger.Named("server.http"), cfg: cfg, failures: make(chan error, 1)}
	s.srv = &http.Server{
		Addr:              cfg.Address,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		ErrorLog:          zap.NewStdLog(s.logger.WithOptions(zap.IncreaseLevel(zap.WarnLevel))),
	}
	return s
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Address)
	if err != nil {
		return err
	}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.failures <- err
		}
	}()
	s.started = true
	s.logger.Info("started", zap.String("address", s.cfg.Address))
	return nil
}

func (s *Server) Failures() <-chan error { return s.failures }

func (s *Server) Stop() {
	if s == nil || !s.started {
		return
	}
	s.stopOnce.Do(func() {
		s.logger.Info("stopping")
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		if err := s.srv.Shutdown(ctx); err != nil {
			s.logger.Warn("shutdown did not finish", zap.Error(err))
			_ = s.srv.Close()
		}
		s.logger.Info("stopped")
	})
}
