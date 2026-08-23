package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// newServeCmd keeps `lode serve` working for existing installations.
func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "serve",
		Short:              "Run the worklode HTTP server",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSibling(cmd.Context(), "lode-server", "server", args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().String("dsn", os.Getenv("LODE_DSN"), "Postgres DSN (postgres://...); defaults to $LODE_DSN")
	cmd.Flags().String("listen", ":8080", "address for the public app server (web UI, API, webhooks)")
	cmd.Flags().String("admin-listen", ":9090", "address for the admin server (/healthz, /metrics)")
	return cmd
}

func init() { rootCmd.AddCommand(newServeCmd()) }
