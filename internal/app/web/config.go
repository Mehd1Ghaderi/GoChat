package web

import (
	"errors"
	"fmt"

	"github.com/ssenerg/gochat/internal/client/drpc"
	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/utils/logging"
	"github.com/ssenerg/gochat/internal/web"
	"github.com/ssenerg/gochat/internal/web/gateway"
)

type Config struct {
	Logging *logging.Config `mapstructure:"logging"`
	Web     *web.Config     `mapstructure:"web"`
	Client  *ClientConfig   `mapstructure:"client"`
	Gateway *gateway.Config `mapstructure:"gateway"`
}

type ClientConfig struct {
	DRPC *drpc.Config `mapstructure:"drpc"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.Logging == nil || c.Web == nil || c.Client == nil || c.Client.DRPC == nil || c.Gateway == nil {
		return errors.New("logging, web, client.drpc and gateway are required")
	}
	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("logging.%w", err)
	}
	if err := c.Web.Validate(); err != nil {
		return fmt.Errorf("web.%w", err)
	}
	if err := c.Client.DRPC.Validate(); err != nil {
		return fmt.Errorf("client.drpc.%w", err)
	}
	if err := c.Gateway.Validate(); err != nil {
		return fmt.Errorf("gateway.%w", err)
	}
	return nil
}
