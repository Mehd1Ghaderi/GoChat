// Package admin serves health, readiness and metrics, on a listener meant to
// stay inside the deployment's network.
package admin

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// Check reports whether a dependency is usable.
type Check struct {
	Name  string
	Ready func(ctx context.Context) error
}

type Server struct {
	logger   *zap.Logger
	cfg      *Config
	srv      *http.Server
	failures chan error
	stopOnce sync.Once
}

func New(logger *zap.Logger, cfg *Config, reg *prometheus.Registry, checks ...Check) *Server {
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	s := &Server{logger: logger.Named("server.admin"), cfg: cfg, failures: make(chan error, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		for _, c := range checks {
			if err := c.Ready(ctx); err != nil {
				s.logger.Warn("not ready", zap.String("check", c.Name), zap.Error(err))
				http.Error(w, c.Name+" is not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	s.srv = &http.Server{
		Addr:              cfg.Address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

func (s *Server) Start() error {
	if !s.cfg.Enabled {
		return nil
	}
	ln, err := net.Listen("tcp", s.cfg.Address)
	if err != nil {
		return err
	}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.failures <- err
		}
	}()
	s.logger.Info("started", zap.String("address", s.cfg.Address))
	return nil
}

func (s *Server) Failures() <-chan error { return s.failures }

func (s *Server) Stop() {
	if s == nil || !s.cfg.Enabled {
		return
	}
	s.stopOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		if err := s.srv.Shutdown(ctx); err != nil {
			s.logger.Warn("shutdown failed", zap.Error(err))
		}
	})
}
