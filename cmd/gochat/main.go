package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/ssenerg/gochat/cmd/migrate"
	"github.com/ssenerg/gochat/cmd/serve"
	"github.com/ssenerg/gochat/cmd/version"
	"github.com/ssenerg/gochat/cmd/web"
	"github.com/ssenerg/gochat/internal"
)

var root = &cobra.Command{
	Use:          internal.AppName(),
	Short:        internal.AppNamePretty(),
	Version:      internal.Version(),
	SilenceUsage: true,
	Run:          func(_ *cobra.Command, _ []string) { version.Header() },
}

func init() {
	root.AddCommand(version.Cmd)
	root.AddCommand(migrate.Cmd)
	root.AddCommand(serve.Cmd)
	root.AddCommand(web.Cmd)
}

func main() {
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
