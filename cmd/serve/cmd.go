package serve

import (
	"github.com/spf13/cobra"

	"github.com/ssenerg/gochat/internal"
)

var (
	Cmd = &cobra.Command{
		Use:   "server",
		Short: "Start the " + internal.AppNamePretty() + " chat server",
		Args:  cobra.NoArgs,
		RunE:  Server,
	}
	configEnvPrefix string
	configPath      string
	configName      string
)

func init() {
	Cmd.Flags().StringVarP(&configEnvPrefix, "config-env-prefix", "e", "", "Environment variable prefix for the config (example: GOCHAT)")
	Cmd.Flags().StringVarP(&configPath, "config-path", "p", "config", "Path to the config file")
	Cmd.Flags().StringVarP(&configName, "config-name", "n", "server", "Name of the config file")
}
