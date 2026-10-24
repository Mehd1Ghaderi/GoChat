package gateway

import (
	"errors"
	"regexp"
	"time"

	"github.com/ssenerg/gochat/internal/config"
)

type Config struct {
	// ID names this gateway in the server's logs; empty uses the hostname.
	ID           string        `mapstructure:"id"`
	ReconnectMin time.Duration `mapstructure:"reconnect_min"`
	ReconnectMax time.Duration `mapstructure:"reconnect_max"`
	// ReadLimit bounds one message a browser sends.
	ReadLimit int64 `mapstructure:"read_limit"`
	// SendBuffer is how many bytes a slow browser may lag behind before its
	// socket is closed, and RecvBuffer how many it may send that its session
	// has yet to handle.
	SendBuffer   int           `mapstructure:"send_buffer"`
	RecvBuffer   int64         `mapstructure:"recv_buffer"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
	PingInterval time.Duration `mapstructure:"ping_interval"`
	// MaxPerUser bounds the sockets one person may hold open.
	MaxPerUser int `mapstructure:"max_per_user"`
	// FrameRate and FrameBurst bound how fast a browser may send.
	FrameRate  float64 `mapstructure:"frame_rate"`
	FrameBurst int     `mapstructure:"frame_burst"`
	// AttachRate and AttachBurst pace the sockets attached to the server,
	// and the Resync settings the pages reloaded after their socket came
	// back, so a restart does not bring every page back at once.
	AttachRate   float64       `mapstructure:"attach_rate"`
	AttachBurst  int           `mapstructure:"attach_burst"`
	ResyncRate   float64       `mapstructure:"resync_rate"`
	ResyncBurst  int           `mapstructure:"resync_burst"`
	ResyncJitter time.Duration `mapstructure:"resync_jitter"`
}

var _ config.Config = (*Config)(nil)

var idRe = regexp.MustCompile(`^[A-Za-z0-9._-]{0,64}$`)

func (c *Config) Validate() error {
	if !idRe.MatchString(c.ID) {
		return errors.New("id must be up to 64 letters, digits, '.', '_' or '-'")
	}
	if c.ReconnectMin <= 0 || c.ReconnectMax < c.ReconnectMin {
		return errors.New("reconnect_min must be positive and at most reconnect_max")
	}
	if c.ReadLimit < 1024 || c.ReadLimit > 1<<20 {
		return errors.New("read_limit must be between 1KiB and 1MiB")
	}
	if c.SendBuffer < 64<<10 || c.SendBuffer > 64<<20 {
		return errors.New("send_buffer must be between 64KiB and 64MiB")
	}
	if c.RecvBuffer < c.ReadLimit || c.RecvBuffer > 64<<20 {
		return errors.New("recv_buffer must be between read_limit and 64MiB")
	}
	if c.WriteTimeout <= 0 || c.PingInterval <= 0 {
		return errors.New("write_timeout and ping_interval must be positive")
	}
	if c.MaxPerUser < 1 {
		return errors.New("max_per_user must be at least 1")
	}
	if c.FrameRate <= 0 || c.FrameBurst < 1 {
		return errors.New("frame_rate and frame_burst must be positive")
	}
	if c.AttachRate <= 0 || c.AttachBurst < 1 || c.ResyncRate <= 0 || c.ResyncBurst < 1 {
		return errors.New("attach_rate, attach_burst, resync_rate and resync_burst must be positive")
	}
	if c.ResyncJitter < 0 || c.ResyncJitter > time.Minute {
		return errors.New("resync_jitter must be between 0 and 1m")
	}
	return nil
}
