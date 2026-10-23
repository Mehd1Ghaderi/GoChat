package web

import (
	"github.com/spf13/cobra"

	"github.com/ssenerg/gochat/internal"
)

var (
	Cmd = &cobra.Command{
		Use:   "web",
		Short: "Start the " + internal.AppNamePretty() + " web gateway",
		Args:  cobra.NoArgs,
		RunE:  Web,
	}
	configEnvPrefix string
	configPath      string
	configName      string
)

func init() {
	Cmd.Flags().StringVarP(&configEnvPrefix, "config-env-prefix", "e", "", "Environment variable prefix for the config (example: GOCHAT)")
	Cmd.Flags().StringVarP(&configPath, "config-path", "p", "config", "Path to the config file")
	Cmd.Flags().StringVarP(&configName, "config-name", "n", "web", "Name of the config file")
}

func Web(_ *cobra.Command, _ []string) error { return nil }
