package web

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/utils"
)

type Config struct {
	// PublicURL is the origin browsers use; https makes every cookie Secure.
	PublicURL string `mapstructure:"public_url"`
	// TrustedProxies are the CIDRs whose X-Forwarded-For is believed.
	TrustedProxies  []string         `mapstructure:"trusted_proxies"`
	UploadLimit     int64            `mapstructure:"upload_limit"`
	RenderCacheSize int              `mapstructure:"render_cache_size"`
	HTTP            *HTTPConfig      `mapstructure:"http"`
	RateLimit       *RateLimitConfig `mapstructure:"rate_limit"`
}

type HTTPConfig struct {
	Address           string        `mapstructure:"address"`
	ReadHeaderTimeout time.Duration `mapstructure:"read_header_timeout"`
	ReadTimeout       time.Duration `mapstructure:"read_timeout"`
	WriteTimeout      time.Duration `mapstructure:"write_timeout"`
	IdleTimeout       time.Duration `mapstructure:"idle_timeout"`
	// TransferTimeout bounds one upload or download, and TransferIdleTimeout
	// how long an upload may send nothing.
	TransferTimeout     time.Duration `mapstructure:"transfer_timeout"`
	TransferIdleTimeout time.Duration `mapstructure:"transfer_idle_timeout"`
	MaxHeaderBytes      int           `mapstructure:"max_header_bytes"`
	ShutdownTimeout     time.Duration `mapstructure:"shutdown_timeout"`
}

type RateLimitConfig struct {
	// Auth bounds sign-in, sign-up, second-factor and invite attempts per IP.
	AuthPerMinute float64 `mapstructure:"auth_per_minute"`
	AuthBurst     int     `mapstructure:"auth_burst"`
	// Requests bounds every request per IP.
	RequestsPerSecond float64 `mapstructure:"requests_per_second"`
	RequestBurst      int     `mapstructure:"request_burst"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	u, err := url.Parse(c.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" && u.Path != "/" {
		return errors.New("public_url must be an http(s) origin, such as https://chat.example.com")
	}
	for _, cidr := range c.TrustedProxies {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("trusted_proxies: %q is not a CIDR", cidr)
		}
	}
	if c.UploadLimit < 1024 {
		return errors.New("upload_limit must be at least 1KiB")
	}
	if c.RenderCacheSize < 1 {
		return errors.New("render_cache_size must be positive")
	}
	if c.HTTP == nil || c.RateLimit == nil {
		return errors.New("http and rate_limit are required")
	}
	if err := utils.ValidateAddress(c.HTTP.Address); err != nil {
		return fmt.Errorf("http.address: %w", err)
	}
	h := c.HTTP
	if h.ReadHeaderTimeout <= 0 || h.ReadTimeout <= 0 || h.WriteTimeout <= 0 || h.IdleTimeout <= 0 ||
		h.TransferTimeout <= 0 || h.TransferIdleTimeout <= 0 || h.ShutdownTimeout <= 0 {
		return errors.New("http timeouts must be positive")
	}
	if h.MaxHeaderBytes < 4096 {
		return errors.New("http.max_header_bytes must be at least 4096")
	}
	r := c.RateLimit
	if r.AuthPerMinute <= 0 || r.AuthBurst < 1 || r.RequestsPerSecond <= 0 || r.RequestBurst < 1 {
		return errors.New("rate_limit values must be positive")
	}
	return nil
}
