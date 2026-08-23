package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// newMigrateCmd keeps `lode migrate` working for existing installations.
func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "migrate",
		Short:              "Apply database migrations",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSibling(cmd.Context(), "lode-migrate", "server", args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().String("dsn", os.Getenv("LODE_DSN"), "Postgres DSN (postgres://...); defaults to $LODE_DSN")
	cmd.Flags().String("migrations-path", "", "path to the directory containing *.up.sql/*.down.sql migration files")
	_ = cmd.MarkFlagRequired("migrations-path")
	return cmd
}

func init() { rootCmd.AddCommand(newMigrateCmd()) }
