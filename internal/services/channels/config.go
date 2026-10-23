package channels

import (
	"errors"
	"time"

	"github.com/ssenerg/gochat/internal/config"
)

type Config struct {
	// IdleTimeout is how long a channel's actor outlives its last request.
	IdleTimeout      time.Duration `mapstructure:"idle_timeout"`
	OpTimeout        time.Duration `mapstructure:"op_timeout"`
	MaxMessageLength int           `mapstructure:"max_message_length"`
	MaxAttachments   int           `mapstructure:"max_attachments"`
	HistoryLimit     int           `mapstructure:"history_limit"`
	TypingInterval   time.Duration `mapstructure:"typing_interval"`
	// PostRate and PostBurst bound how fast one person may post in a channel.
	PostRate  float64 `mapstructure:"post_rate"`
	PostBurst int     `mapstructure:"post_burst"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.IdleTimeout < time.Second || c.OpTimeout <= 0 {
		return errors.New("idle_timeout and op_timeout must be positive")
	}
	if c.MaxMessageLength < 1 || c.MaxMessageLength > 65535 {
		return errors.New("max_message_length must be between 1 and 65535")
	}
	if c.MaxAttachments < 0 || c.MaxAttachments > 50 {
		return errors.New("max_attachments must be between 0 and 50")
	}
	if c.HistoryLimit < 1 || c.HistoryLimit > 200 {
		return errors.New("history_limit must be between 1 and 200")
	}
	if c.TypingInterval <= 0 {
		return errors.New("typing_interval must be positive")
	}
	if c.PostRate <= 0 || c.PostBurst < 1 {
		return errors.New("post_rate and post_burst must be positive")
	}
	return nil
}
