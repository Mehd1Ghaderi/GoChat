package server

import (
	"errors"
	"fmt"

	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/repos"
	"github.com/ssenerg/gochat/internal/server"
	"github.com/ssenerg/gochat/internal/services"
	"github.com/ssenerg/gochat/internal/utils/logging"
)

type Config struct {
	Logging  *logging.Config      `mapstructure:"logging"`
	Server   *server.Config       `mapstructure:"server"`
	Database *repos.Config        `mapstructure:"database"`
	Storage  *repos.StorageConfig `mapstructure:"storage"`
	Services *services.Config     `mapstructure:"services"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.Logging == nil || c.Server == nil || c.Database == nil || c.Storage == nil || c.Services == nil {
		return errors.New("logging, server, database, storage and services are required")
	}
	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("logging.%w", err)
	}
	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("server.%w", err)
	}
	if err := c.Database.Validate(); err != nil {
		return fmt.Errorf("database.%w", err)
	}
	if err := c.Storage.Validate(); err != nil {
		return fmt.Errorf("storage.%w", err)
	}
	if err := c.Services.Validate(); err != nil {
		return fmt.Errorf("services.%w", err)
	}
	return nil
}
