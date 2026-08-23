package cmd

import "github.com/spf13/cobra"

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{Use: "migrate", Short: "Apply database migrations", DisableFlagParsing: true, RunE: func(cmd *cobra.Command, args []string) error {
		return runSibling(cmd.Context(), "lode-migrate", "server", args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	}}
}

func init() {
	rootCmd.AddCommand(newMigrateCmd())
}
