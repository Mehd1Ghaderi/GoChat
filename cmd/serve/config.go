package serve

import (
	_ "embed"

	"github.com/ssenerg/gochat/internal/app/server"
)

//go:embed default.yaml
var defaultConfig []byte

type Config server.Config

func (c *Config) Validate() error { return (*server.Config)(c).Validate() }
