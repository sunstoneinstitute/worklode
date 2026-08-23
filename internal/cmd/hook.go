package cmd

import "github.com/spf13/cobra"

func init() { rootCmd.AddCommand(newHookCmd()) }

// newHookCmd keeps `lode hook` working for existing installations.
func newHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "hook <event> [--harness <id>] [--next <cmd> [arg...]] | hook --list",
		Short:              "Run a Worklode lifecycle hook (--list shows every event)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSibling(cmd.Context(), "lode-hook", "user", args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}
