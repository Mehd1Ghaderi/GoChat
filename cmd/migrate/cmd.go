package migrate

import (
	"github.com/spf13/cobra"
)

var (
	Cmd = &cobra.Command{
		Use:   "migrate",
		Short: "Migrate the database schemas",
	}

	scyllaCmd = &cobra.Command{
		Use:   "scylla",
		Short: "Migrate scylla schemas",
	}

	scyllaUpCmd = &cobra.Command{
		Use:   "up <migrations-dir>",
		Short: "Create the keyspace if needed and apply pending migrations",
		RunE:  scyllaUp,
		Args:  cobra.ExactArgs(1),
	}

	scyllaStatusCmd = &cobra.Command{
		Use:   "status <migrations-dir>",
		Short: "Print the applied and pending migrations",
		RunE:  scyllaStatus,
		Args:  cobra.ExactArgs(1),
	}

	configEnvPrefix string
	configPath      string
	configName      string
)

func init() {
	Cmd.PersistentFlags().StringVarP(&configEnvPrefix, "config-env-prefix", "e", "", "Environment variable prefix for the config (example: GOCHAT)")
	Cmd.PersistentFlags().StringVarP(&configPath, "config-path", "p", "config", "Path to the config file")
	Cmd.PersistentFlags().StringVarP(&configName, "config-name", "n", "migrate", "Name of the config file")

	scyllaCmd.AddCommand(scyllaUpCmd, scyllaStatusCmd)
	Cmd.AddCommand(scyllaCmd)
}
