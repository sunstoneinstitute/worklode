package cmd

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/hookrun"
)

// This file wires `lode hook <event>` to the logic in internal/hookrun. It is
// deliberately thin: it splits the event from an optional `--next <cmd>
// [arg...]` daisy-chain and hands everything else off. Flag parsing is
// disabled so the downstream command's own flags pass through verbatim.

func init() {
	rootCmd.AddCommand(newHookCmd())
}

func newHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hook <event> [--next <cmd> [arg...]]",
		Short: "Run a Worklode lifecycle hook (session-start|heartbeat|session-end|pre-commit|worktree-create|worktree-remove|worktree-enter|worktree-exit)",
		Long: "Backbone lifecycle hooks that keep a worktree's lease alive around a coding " +
			"session. Reads the hook payload on stdin, does nothing outside a wt/<id>-<slug> " +
			"worktree, and never fails the triggering event. With --next, it also runs the " +
			"downstream command (composing with an existing hook), replaying the payload on " +
			"its stdin and propagating its exit code.",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// DisableFlagParsing also suppresses cobra's own -h/--help, so
			// intercept it here (it can only appear in the event position; a
			// downstream --next argv is taken verbatim and never reaches here).
			if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
				return cmd.Help()
			}
			event, next, err := parseHookArgs(args)
			if err != nil {
				return err
			}
			code := hookrun.Run(cmd.Context(), hookrun.Options{
				Event:  event,
				Next:   next,
				Stdin:  cmd.InOrStdin(),
				Stdout: cmd.OutOrStdout(),
				Stderr: cmd.ErrOrStderr(),
			})
			// A non-zero code is the daisy-chained child's exit code; propagate
			// it. Worklode's own actions never produce a non-zero code.
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
}

// parseHookArgs splits `<event> [--next cmd arg...]`. Everything after the
// literal "--next" is the downstream argv, taken verbatim.
func parseHookArgs(args []string) (event string, next []string, err error) {
	if len(args) == 0 {
		return "", nil, errors.New("hook requires an event argument")
	}
	event = args[0]
	for i, a := range args[1:] {
		if a == "--next" {
			next = args[1+i+1:]
			if len(next) == 0 {
				return "", nil, errors.New("--next requires a command")
			}
			return event, next, nil
		}
	}
	return event, nil, nil
}
