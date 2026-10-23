package minio

import (
	"errors"

	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/utils"
)

type Config struct {
	Endpoint  string `mapstructure:"endpoint"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	Bucket    string `mapstructure:"bucket"`
	Region    string `mapstructure:"region"`
	UseSSL    bool   `mapstructure:"use_ssl"`
	// PartSize bounds the memory one upload of unknown size buffers.
	PartSize uint64 `mapstructure:"part_size"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if err := utils.ValidateAddress(c.Endpoint); err != nil {
		return errors.New("endpoint must be host:port")
	}
	if c.AccessKey == "" || c.SecretKey == "" {
		return errors.New("access_key and secret_key are required")
	}
	if len(c.Bucket) < 3 || len(c.Bucket) > 63 {
		return errors.New("bucket must be 3-63 characters")
	}
	if c.PartSize < 5<<20 {
		return errors.New("part_size must be at least 5MiB")
	}
	return nil
}
