package accounts

import (
	"errors"
	"fmt"
	"time"

	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/core/password"
	"github.com/ssenerg/gochat/internal/core/secure"
)

type Config struct {
	Workers           int             `mapstructure:"workers"`
	OpTimeout         time.Duration   `mapstructure:"op_timeout"`
	Password          password.Params `mapstructure:"password"`
	MinPasswordLength int             `mapstructure:"min_password_length"`
	SessionTTL        time.Duration   `mapstructure:"session_ttl"`
	SessionCacheTTL   time.Duration   `mapstructure:"session_cache_ttl"`
	SessionCacheSize  int             `mapstructure:"session_cache_size"`
	MFATTL            time.Duration   `mapstructure:"mfa_ttl"`
	MFAMaxAttempts    int             `mapstructure:"mfa_max_attempts"`
	TOTPIssuer        string          `mapstructure:"totp_issuer"`
	// SecretKey seals TOTP secrets at rest: 32 bytes, base64.
	SecretKey     string        `mapstructure:"secret_key"`
	OpenSignup    bool          `mapstructure:"open_signup"`
	LoginFailures int           `mapstructure:"login_failures"`
	LoginWindow   time.Duration `mapstructure:"login_window"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.Workers < 1 {
		return errors.New("workers must be at least 1")
	}
	if c.OpTimeout <= 0 {
		return errors.New("op_timeout must be positive")
	}
	if err := c.Password.Validate(); err != nil {
		return fmt.Errorf("password.%w", err)
	}
	if c.MinPasswordLength < 8 {
		return errors.New("min_password_length must be at least 8")
	}
	if c.SessionTTL < time.Minute || c.MFATTL < 30*time.Second {
		return errors.New("session_ttl and mfa_ttl are too short")
	}
	if c.SessionCacheTTL <= 0 || c.SessionCacheSize < 1 {
		return errors.New("session_cache_ttl and session_cache_size must be positive")
	}
	if c.MFAMaxAttempts < 1 {
		return errors.New("mfa_max_attempts must be at least 1")
	}
	if c.TOTPIssuer == "" {
		return errors.New("totp_issuer is required")
	}
	if _, err := secure.DecodeKey(c.SecretKey); err != nil {
		return fmt.Errorf("secret_key: %w", err)
	}
	if c.LoginFailures < 1 || c.LoginWindow <= 0 {
		return errors.New("login_failures and login_window must be positive")
	}
	return nil
}
