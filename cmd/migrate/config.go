package migrate

import (
	_ "embed"
	"errors"
	"fmt"

	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/repos"
	"github.com/ssenerg/gochat/internal/utils/logging"
)

//go:embed default.yaml
var defaultConfig []byte

type Config struct {
	Logging  *logging.Config `mapstructure:"logging"`
	Database *repos.Config   `mapstructure:"database"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.Logging == nil || c.Database == nil {
		return errors.New("logging and database are required")
	}
	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("logging.%w", err)
	}
	if err := c.Database.Validate(); err != nil {
		return fmt.Errorf("database.%w", err)
	}
	return nil
}
