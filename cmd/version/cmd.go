package version

import (
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:          "version",
	Short:        "Print the application information",
	SilenceUsage: true,
	Run:          func(_ *cobra.Command, _ []string) { Header() },
}
