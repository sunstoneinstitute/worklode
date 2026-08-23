package cmd

import "github.com/spf13/cobra"

func init() { rootCmd.AddCommand(newStatuslineCmd()) }

// newStatuslineCmd keeps `lode statusline` working for existing installations.
func newStatuslineCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "statusline",
		Short: "Render one status line from a coding agent's status-line payload",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSibling(cmd.Context(), "lode-statusline", "user", args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}
