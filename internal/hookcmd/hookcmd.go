// Package hookcmd implements the lode-hook executable.
package hookcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/githooks"
	"github.com/sunstoneinstitute/worklode/internal/harness"
	"github.com/sunstoneinstitute/worklode/internal/hookrun"
)

// Run executes a lifecycle hook and returns its exit status.
func Run(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(argv) > 0 && argv[0] == "--list" {
		printEvents(stdout)
		return 0
	}
	event, harnessID, args, next, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return hookrun.Run(ctx, hookrun.Options{Event: event, Args: args, Harness: harnessID, Next: next, Stdin: stdin, Stdout: stdout, Stderr: stderr})
}

func parseArgs(argv []string) (event, harnessID string, args, next []string, err error) {
	if len(argv) == 0 {
		return "", "", nil, nil, errors.New("hook requires an event argument")
	}
	event, rest := argv[0], argv[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--next":
			next = rest[i+1:]
			if len(next) == 0 {
				return "", "", nil, nil, errors.New("--next requires a command")
			}
			return event, harnessID, args, next, nil
		case "--harness":
			if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "--") {
				return "", "", nil, nil, errors.New("--harness requires a harness id")
			}
			harnessID, i = rest[i+1], i+1
		default:
			args = append(args, rest[i])
		}
	}
	return event, harnessID, args, nil, nil
}

const unboundTrigger = "(unbound — callable from scripts)"

func hookTriggers() map[string]string {
	byEvent := map[string][]string{}
	for _, id := range harness.IDs() {
		h, ok := harness.Get(id)
		if !ok {
			continue
		}
		for event, natives := range h.Events() {
			byEvent[string(event)] = append(byEvent[string(event)], id+" "+strings.Join(natives, ", "))
		}
	}
	triggers := map[string]string{}
	for _, h := range githooks.Managed {
		triggers[h.Name] = "git " + h.Name
	}
	for event, entries := range byEvent {
		triggers[event] = strings.Join(entries, "; ")
	}
	return triggers
}

func printEvents(w io.Writer) {
	triggers, events, width := hookTriggers(), hookrun.Events(), 0
	for _, event := range events {
		width = max(width, len(event.Name))
	}
	fmt.Fprint(w, "Worklode lifecycle hooks — `lode hook <event>`, payload on stdin:\n\n")
	for _, event := range events {
		trigger := triggers[event.Name]
		if trigger == "" {
			trigger = unboundTrigger
		}
		fmt.Fprintf(w, "  %-*s  %s\n  %-*s  %s\n\n", width, event.Name, trigger, width, "", event.Summary)
	}
	fmt.Fprint(w, "Bind them all with `lode install`; compose with an existing hook using --next.\n")
}
