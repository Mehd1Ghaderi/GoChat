package directory

import (
	"errors"
	"time"

	"github.com/ssenerg/gochat/internal/config"
)

type Config struct {
	Workers   int           `mapstructure:"workers"`
	OpTimeout time.Duration `mapstructure:"op_timeout"`
	InviteTTL time.Duration `mapstructure:"invite_ttl"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.Workers < 1 {
		return errors.New("workers must be at least 1")
	}
	if c.OpTimeout <= 0 {
		return errors.New("op_timeout must be positive")
	}
	if c.InviteTTL < time.Minute || c.InviteTTL > 30*24*time.Hour {
		return errors.New("invite_ttl must be between 1m and 720h")
	}
	return nil
}
