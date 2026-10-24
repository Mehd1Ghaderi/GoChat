package index

import (
	"errors"
	"time"

	"github.com/ssenerg/gochat/internal/config"
)

type Config struct {
	// Workers bound the searches running at once, and Writers the messages
	// being filed at once.
	Workers   int           `mapstructure:"workers"`
	Writers   int           `mapstructure:"writers"`
	OpTimeout time.Duration `mapstructure:"op_timeout"`
	// Rate and Burst bound how often one person searches.
	Rate  float64 `mapstructure:"rate"`
	Burst int     `mapstructure:"burst"`
	// Backfill files, once, the messages written before search existed.
	Backfill bool `mapstructure:"backfill"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.Workers < 1 || c.Writers < 1 {
		return errors.New("workers and writers must be at least 1")
	}
	if c.OpTimeout <= 0 {
		return errors.New("op_timeout must be positive")
	}
	if c.Rate <= 0 || c.Burst < 1 {
		return errors.New("rate and burst must be positive")
	}
	return nil
}
