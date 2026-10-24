package files

import (
	"errors"
	"time"

	"github.com/ssenerg/gochat/internal/config"
)

type Config struct {
	Workers   int           `mapstructure:"workers"`
	OpTimeout time.Duration `mapstructure:"op_timeout"`
	MaxSize   int64         `mapstructure:"max_size"`
	// Quota bounds the bytes of attachments one user keeps; zero is no
	// limit. A system admin can give a user another.
	Quota int64 `mapstructure:"quota"`
	// MaxVoiceDuration bounds how long a voice message may be, and
	// MaxVoiceSize how large, below MaxSize, which bounds it too: a
	// recording that plays in place must stay a recording.
	MaxVoiceDuration time.Duration `mapstructure:"max_voice_duration"`
	MaxVoiceSize     int64         `mapstructure:"max_voice_size"`
	// MaxUploads bounds the uploads streaming at once, and MaxUserUploads
	// those of one user.
	MaxUploads     int `mapstructure:"max_uploads"`
	MaxUserUploads int `mapstructure:"max_user_uploads"`
	// OrphanAfter is how long an upload may wait to be posted before the
	// janitor deletes it.
	OrphanAfter     time.Duration `mapstructure:"orphan_after"`
	JanitorInterval time.Duration `mapstructure:"janitor_interval"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.Workers < 1 || c.MaxUploads < 1 || c.MaxUserUploads < 1 {
		return errors.New("workers, max_uploads and max_user_uploads must be at least 1")
	}
	if c.OpTimeout <= 0 {
		return errors.New("op_timeout must be positive")
	}
	if c.MaxSize < 1024 || c.MaxSize > 5<<30 {
		return errors.New("max_size must be between 1KiB and 5GiB")
	}
	if c.Quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if c.MaxVoiceDuration < time.Second || c.MaxVoiceDuration > time.Hour {
		return errors.New("max_voice_duration must be between 1s and 1h")
	}
	if c.MaxVoiceSize < 1024 {
		return errors.New("max_voice_size must be at least 1KiB")
	}
	if c.OrphanAfter < time.Hour || c.JanitorInterval < time.Minute {
		return errors.New("orphan_after must be at least 1h and janitor_interval at least 1m")
	}
	return nil
}
