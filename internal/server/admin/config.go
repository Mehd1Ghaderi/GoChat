package admin

import (
	"errors"
	"time"

	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/utils"
)

type Config struct {
	Enabled         bool          `mapstructure:"enabled"`
	Address         string        `mapstructure:"address"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if err := utils.ValidateAddress(c.Address); err != nil {
		return err
	}
	if c.ShutdownTimeout <= 0 {
		return errors.New("shutdown_timeout must be positive")
	}
	return nil
}
