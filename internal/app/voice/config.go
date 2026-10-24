package voice

import (
	"errors"
	"fmt"

	"github.com/ssenerg/gochat/internal/client/drpc"
	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/utils/logging"
	"github.com/ssenerg/gochat/internal/voiceserver"
)

type Config struct {
	Logging *logging.Config     `mapstructure:"logging"`
	Voice   *voiceserver.Config `mapstructure:"voice"`
	Client  *ClientConfig       `mapstructure:"client"`
}

type ClientConfig struct {
	DRPC *drpc.Config `mapstructure:"drpc"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.Logging == nil || c.Voice == nil || c.Client == nil || c.Client.DRPC == nil {
		return errors.New("logging, voice and client.drpc are required")
	}
	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("logging.%w", err)
	}
	if err := c.Voice.Validate(); err != nil {
		return fmt.Errorf("voice.%w", err)
	}
	if err := c.Client.DRPC.Validate(); err != nil {
		return fmt.Errorf("client.drpc.%w", err)
	}
	return nil
}
