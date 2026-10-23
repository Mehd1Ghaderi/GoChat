package repos

import (
	"errors"
	"fmt"

	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/repos/minio"
	"github.com/ssenerg/gochat/internal/repos/scylla"
)

type Config struct {
	Scylla *scylla.Config `mapstructure:"scylla"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.Scylla == nil {
		return errors.New("scylla is required")
	}
	if err := c.Scylla.Validate(); err != nil {
		return fmt.Errorf("scylla.%w", err)
	}
	return nil
}

type StorageConfig struct {
	Minio *minio.Config `mapstructure:"minio"`
}

var _ config.Config = (*StorageConfig)(nil)

func (c *StorageConfig) Validate() error {
	if c.Minio == nil {
		return errors.New("minio is required")
	}
	if err := c.Minio.Validate(); err != nil {
		return fmt.Errorf("minio.%w", err)
	}
	return nil
}
