package server

import (
	"errors"
	"fmt"

	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/server/admin"
	"github.com/ssenerg/gochat/internal/server/drpc"
)

type Config struct {
	DRPC  *drpc.Config  `mapstructure:"drpc"`
	Admin *admin.Config `mapstructure:"admin"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.DRPC == nil || c.Admin == nil {
		return errors.New("drpc and admin are required")
	}
	if err := c.DRPC.Validate(); err != nil {
		return fmt.Errorf("drpc.%w", err)
	}
	if err := c.Admin.Validate(); err != nil {
		return fmt.Errorf("admin.%w", err)
	}
	return nil
}
