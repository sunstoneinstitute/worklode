package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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
		Use:   "hook <event> [--next <cmd> [arg...]] | hook --list",
		Short: "Run a Worklode lifecycle hook (--list shows every event)",
		Long: "Backbone lifecycle hooks that keep a worktree's lease alive around a coding " +
			"session. Reads the hook payload on stdin, does nothing outside a Worklode worktree, " +
			"and never fails the triggering event. With --next, it also runs the " +
			"downstream command (composing with an existing hook), replaying the payload on " +
			"its stdin and propagating its exit code.\n\n" +
			"`lode hook --list` prints every supported event, what `lode install` binds it to, " +
			"and what its handler does.",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// DisableFlagParsing also suppresses cobra's own -h/--help, so
			// intercept it here (it can only appear in the event position; a
			// downstream --next argv is taken verbatim and never reaches here).
			// --list is read the same way, for the same reason.
			if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
				return cmd.Help()
			}
			if len(args) > 0 && args[0] == "--list" {
				printHookEvents(cmd.OutOrStdout())
				return nil
			}
			event, hookArgs, next, err := parseHookArgs(args)
			if err != nil {
				return err
			}
			code := hookrun.Run(cmd.Context(), hookrun.Options{
				Event:  event,
				Args:   hookArgs,
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

// parseHookArgs splits `<event> [arg...] [--next cmd arg...]` three ways: the
// event, the hook's own arguments, and the downstream argv. Everything after
// the literal "--next" is the downstream argv, taken verbatim; everything
// before it is the hook's own (git's positional arguments, for the hooks that
// read them — commit-msg's message file is $1).
func parseHookArgs(argv []string) (event string, args, next []string, err error) {
	if len(argv) == 0 {
		return "", nil, nil, errors.New("hook requires an event argument")
	}
	event = argv[0]
	for i, a := range argv[1:] {
		if a == "--next" {
			next = argv[1+i+1:]
			if len(next) == 0 {
				return "", nil, nil, errors.New("--next requires a command")
			}
			return event, argv[1 : 1+i], next, nil
		}
	}
	return event, argv[1:], nil, nil
}

// unboundTrigger labels an event nothing installs a binding for. It is not a
// deprecation: the handler works when called, it just has no harness event
// behind it, so only a script can reach it.
const unboundTrigger = "(unbound — callable from scripts)"

// hookTriggers maps each Worklode event to what a default `lode install` binds
// it to. Derived from claudeBindings and from gitHooks — the same lists
// installation walks — rather than restated, so the listing cannot drift from
// what installation actually wires up.
func hookTriggers() map[string]string {
	claude := map[string][]string{}
	for _, b := range claudeBindings {
		event := strings.TrimPrefix(b.Command, lodeHookPrefix)
		name := b.Event
		if b.Matcher != "" {
			name += "[" + b.Matcher + "]"
		}
		claude[event] = append(claude[event], name)
	}
	triggers := map[string]string{}
	for _, h := range gitHooks {
		triggers[h.name] = "git " + h.name
	}
	for event, names := range claude {
		triggers[event] = "Claude Code " + strings.Join(names, ", ")
	}
	return triggers
}

// printHookEvents renders `lode hook --list`: every supported event, what
// triggers it, and what its handler does. Two lines per event — trigger and
// summary side by side would run past 130 columns, and a wrapped table cell
// is unreadable. Padded by hand rather than by a tabwriter, whose blocks end
// at the blank line between events (aligning each event only with itself)
// unless the blank line is given a tab and so trailing whitespace.
func printHookEvents(w io.Writer) {
	triggers := hookTriggers()
	width := 0
	for _, e := range hookrun.Events() {
		width = max(width, len(e.Name)) // event names are ASCII
	}
	fmt.Fprintf(w, "Worklode lifecycle hooks — `lode hook <event>`, payload on stdin:\n\n")
	for _, e := range hookrun.Events() {
		trigger, ok := triggers[e.Name]
		if !ok {
			trigger = unboundTrigger
		}
		fmt.Fprintf(w, "  %-*s  %s\n", width, e.Name, trigger)
		fmt.Fprintf(w, "  %-*s  %s\n\n", width, "", e.Summary)
	}
	fmt.Fprintf(w, "Bind them all with `lode install`; compose with an existing hook using --next.\n")
}
