package voice

import (
	"github.com/spf13/cobra"

	"github.com/ssenerg/gochat/internal"
)

var (
	Cmd = &cobra.Command{
		Use:   "voice",
		Short: "Start the " + internal.AppNamePretty() + " voice server",
		Args:  cobra.NoArgs,
		RunE:  Voice,
	}
	configEnvPrefix string
	configPath      string
	configName      string
)

func init() {
	Cmd.Flags().StringVarP(&configEnvPrefix, "config-env-prefix", "e", "", "Environment variable prefix for the config (example: GOCHAT)")
	Cmd.Flags().StringVarP(&configPath, "config-path", "p", "config", "Path to the config file")
	Cmd.Flags().StringVarP(&configName, "config-name", "n", "voice", "Name of the config file")
}
