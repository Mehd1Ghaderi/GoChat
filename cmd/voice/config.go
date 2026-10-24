package voice

import (
	_ "embed"

	"github.com/ssenerg/gochat/internal/app/voice"
)

//go:embed default.yaml
var defaultConfig []byte

type Config voice.Config

func (c *Config) Validate() error { return (*voice.Config)(c).Validate() }
