package drpc

import (
	"errors"
	"time"

	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/utils"
)

type Config struct {
	Address string `mapstructure:"address"`
	// GatewayKey is the secret every web gateway presents on every call.
	GatewayKey string `mapstructure:"gateway_key"`
	// VoiceServerKey is the secret voice servers present, which opens only
	// their link and never a gateway's calls. Empty refuses voice servers.
	VoiceServerKey    string        `mapstructure:"voice_server_key"`
	TLS               *TLSConfig    `mapstructure:"tls"`
	MaxMessageSize    int           `mapstructure:"max_message_size"`
	MaxConnections    int           `mapstructure:"max_connections"`
	InactivityTimeout time.Duration `mapstructure:"inactivity_timeout"`
	// WriteTimeout bounds how long a write to a gateway may stall.
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	RequestTimeout  time.Duration `mapstructure:"request_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

type TLSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
	// ClientCAFile, when set, requires gateways to present a certificate it
	// signed.
	ClientCAFile string `mapstructure:"client_ca_file"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if err := utils.ValidateAddress(c.Address); err != nil {
		return err
	}
	if len(c.GatewayKey) < 32 {
		return errors.New("gateway_key must be at least 32 characters")
	}
	if c.VoiceServerKey != "" && (len(c.VoiceServerKey) < 32 || c.VoiceServerKey == c.GatewayKey) {
		return errors.New("voice_server_key must be at least 32 characters and differ from gateway_key")
	}
	if c.TLS == nil {
		return errors.New("tls is required")
	}
	if c.TLS.Enabled {
		if err := utils.ValidateFile(c.TLS.CertFile); err != nil {
			return errors.New("tls.cert_file: " + err.Error())
		}
		if err := utils.ValidateFile(c.TLS.KeyFile); err != nil {
			return errors.New("tls.key_file: " + err.Error())
		}
		if c.TLS.ClientCAFile != "" {
			if err := utils.ValidateFile(c.TLS.ClientCAFile); err != nil {
				return errors.New("tls.client_ca_file: " + err.Error())
			}
		}
	}
	if c.MaxMessageSize < 64<<10 {
		return errors.New("max_message_size must be at least 64KiB")
	}
	if c.MaxConnections < 1 {
		return errors.New("max_connections must be at least 1")
	}
	if c.InactivityTimeout < 0 || c.WriteTimeout <= 0 || c.RequestTimeout <= 0 || c.ShutdownTimeout <= 0 {
		return errors.New("timeouts must be positive")
	}
	return nil
}
