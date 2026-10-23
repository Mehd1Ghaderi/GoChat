// Package drpc serves the chat API to the web gateways.
package drpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/net/netutil"
	"storj.io/drpc"
	"storj.io/drpc/drpcmanager"
	"storj.io/drpc/drpcmux"
	"storj.io/drpc/drpcserver"
	"storj.io/drpc/drpcwire"
)

type Server struct {
	logger   *zap.Logger
	cfg      *Config
	srv      *drpcserver.Server
	cancel   context.CancelFunc
	done     chan struct{}
	failures chan error
	stopOnce sync.Once
}

func New(logger *zap.Logger, cfg *Config, mux *drpcmux.Mux, handler func(drpc.Handler) drpc.Handler) *Server {
	s := &Server{logger: logger.Named("server.drpc"), cfg: cfg, failures: make(chan error, 1), done: make(chan struct{})}
	s.srv = drpcserver.NewWithOptions(handler(mux), drpcserver.Options{
		Manager: drpcmanager.Options{
			Reader:            drpcwire.ReaderOptions{MaximumBufferSize: cfg.MaxMessageSize},
			InactivityTimeout: cfg.InactivityTimeout,
		},
		Log: func(err error) { s.logger.Debug("connection error", zap.Error(err)) },
	})
	return s
}

func (s *Server) tlsConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load certificate: %w", err)
	}
	tc := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	if s.cfg.TLS.ClientCAFile != "" {
		pem, err := os.ReadFile(s.cfg.TLS.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("client CA file holds no certificate")
		}
		tc.ClientCAs = pool
		tc.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return tc, nil
}

func (s *Server) Start() error {
	lis, err := net.Listen("tcp", s.cfg.Address)
	if err != nil {
		return err
	}
	if s.cfg.TLS.Enabled {
		tc, err := s.tlsConfig()
		if err != nil {
			_ = lis.Close()
			return err
		}
		lis = tls.NewListener(lis, tc)
	}
	lis = netutil.LimitListener(lis, s.cfg.MaxConnections)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() {
		defer close(s.done)
		if err := s.srv.Serve(ctx, lis); err != nil && ctx.Err() == nil {
			s.failures <- err
		}
	}()
	s.logger.Info("started", zap.String("address", s.cfg.Address), zap.Bool("tls", s.cfg.TLS.Enabled))
	return nil
}

func (s *Server) Failures() <-chan error { return s.failures }

func (s *Server) Stop() {
	if s == nil || s.cancel == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.logger.Info("stopping")
		s.cancel()
		<-s.done
		s.logger.Info("stopped")
	})
}
