package web

import (
	_ "embed"

	"github.com/ssenerg/gochat/internal/app/web"
)

//go:embed default.yaml
var defaultConfig []byte

type Config web.Config

func (c *Config) Validate() error { return (*web.Config)(c).Validate() }
