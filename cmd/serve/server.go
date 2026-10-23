package serve

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ssenerg/gochat/cmd/version"
	"github.com/ssenerg/gochat/internal/app/server"
	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/utils"
)

func Server(cmd *cobra.Command, _ []string) error {
	version.Header()
	cfg := Config{}
	if err := config.LoadConfig(
		&cfg,
		utils.EnvOr("CONFIG_ENV_PREFIX", configEnvPrefix),
		utils.EnvOr("CONFIG_PATH", configPath),
		utils.EnvOr("CONFIG_NAME", configName),
		defaultConfig,
	); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	return server.Run(cmd.Context(), (*server.Config)(&cfg))
}
