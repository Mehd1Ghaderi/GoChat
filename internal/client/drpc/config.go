package drpc

import (
	"errors"
	"time"

	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/utils"
)

type Config struct {
	Address     string        `mapstructure:"address"`
	GatewayKey  string        `mapstructure:"gateway_key"`
	TLS         *TLSConfig    `mapstructure:"tls"`
	DialTimeout time.Duration `mapstructure:"dial_timeout"`
	// WriteTimeout bounds how long a write to the server may stall.
	WriteTimeout   time.Duration `mapstructure:"write_timeout"`
	RequestTimeout time.Duration `mapstructure:"request_timeout"`
	PoolCapacity   int           `mapstructure:"pool_capacity"`
	// PoolExpiration must stay below the server's inactivity_timeout.
	PoolExpiration time.Duration `mapstructure:"pool_expiration"`
	MaxMessageSize int           `mapstructure:"max_message_size"`
}

type TLSConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	CAFile     string `mapstructure:"ca_file"`
	CertFile   string `mapstructure:"cert_file"`
	KeyFile    string `mapstructure:"key_file"`
	ServerName string `mapstructure:"server_name"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if err := utils.ValidateAddress(c.Address); err != nil {
		return err
	}
	if len(c.GatewayKey) < 32 {
		return errors.New("gateway_key must be at least 32 characters")
	}
	if c.TLS == nil {
		return errors.New("tls is required")
	}
	if c.TLS.Enabled && (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") {
		return errors.New("tls.cert_file and tls.key_file go together")
	}
	if c.DialTimeout <= 0 || c.WriteTimeout <= 0 || c.RequestTimeout <= 0 || c.PoolExpiration <= 0 {
		return errors.New("timeouts must be positive")
	}
	if c.PoolCapacity < 1 {
		return errors.New("pool_capacity must be at least 1")
	}
	if c.MaxMessageSize < 64<<10 {
		return errors.New("max_message_size must be at least 64KiB")
	}
	return nil
}
