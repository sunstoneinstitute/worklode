package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// entityPlaceholders is the closed vocabulary a usage string uses to name one
// of the four entity kinds 061 §3 C1 gives a completion helper: task, doc,
// project, actor. `<id>` is whichever of the four the command's own entity
// group makes it (`lode task show <id>` a task, `lode project focus <id>` a
// project); the rule below only asks that *something* completes it.
//
// This is the vocabulary, not a sample of it. A command that spells the same
// argument some other way becomes invisible to the rule, which is why
// `lode approval request` was respelled `<ref>` rather than given a sixth
// entry here (WL-509). Spell a new entity argument with one of these; add an
// entry only for an entity kind that gains a helper.
var entityPlaceholders = map[string]bool{
	"id":      true,
	"task-id": true,
	"ref":     true,
	"project": true,
	"actor":   true,
}

// entityArgsExempt names the commands that take an entity placeholder and
// deliberately offer no candidates, each with the reason completing it would
// hand the user arguments the command refuses. An entry here is a decision;
// the staleness check below stops it from becoming a habit.
var entityArgsExempt = map[string]string{
	"lode project add": "creates a project: the existing keys are exactly the arguments it rejects",
	"lode actor add":   "creates an actor: same as project add",
}

// TestEntityArgsComplete is 061 §3 C1's coverage: a positional argument
// naming an entity has a ValidArgsFunction. Sixty commands were wired by hand
// (WL-506–WL-508) and nothing else would notice the sixty-first arriving
// without one, or an existing one being dropped in a refactor.
//
// It walks the live cobra tree rather than parsing source the way
// renderrule_test.go does, because ValidArgsFunction is a func field with no
// stable spelling to grep for: commands set it from taskIDs, from a
// taskIDAt(n)/docRefAt(n) closure, and from per-command bodies like
// taskSetArgs. Whether the field ends up non-nil is a runtime fact, and the
// built tree is the same source of truth TestCommandReference already uses.
//
// Scope is positional arguments only. A flag's values complete through
// RegisterFlagCompletionFunc (061 §3 C4), which leaves nothing on the command
// this walk can see, so the scan stops at the first flag in a usage string —
// the `<id>` in `lode secret purge [--task <id>]` is not this rule's.
func TestEntityArgsComplete(t *testing.T) {
	seen := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" || sub.Name() == "completion" {
				continue // cobra's own, and neither takes an entity
			}
			path := sub.CommandPath()
			args := entityArgs(sub.Use)
			if len(args) > 0 {
				if reason, exempt := entityArgsExempt[path]; exempt {
					seen[path] = true
					if sub.ValidArgsFunction != nil {
						t.Errorf("%q completes %s but is listed exempt (%s): drop the entityArgsExempt entry",
							path, strings.Join(args, " "), reason)
					}
				} else if sub.ValidArgsFunction == nil {
					t.Errorf("%q takes %s and has no ValidArgsFunction: wire one of the helpers in "+
						"completion.go (061 §3 C1), or add an entityArgsExempt entry saying why "+
						"completing it would offer arguments the command refuses",
						path, strings.Join(args, " "))
				}
			}
			walk(sub)
		}
	}
	walk(rootCmd)
	for path, reason := range entityArgsExempt {
		if !seen[path] {
			t.Errorf("entityArgsExempt names %q (%s), which takes no entity placeholder: "+
				"the command was renamed or respelled, so remove the entry", path, reason)
		}
	}
}

// entityArgs returns the entity placeholders in a usage string's positional
// arguments, in order, as written. The scan stops at the first flag, so
// nothing inside `[--to <actor>]` counts.
func entityArgs(use string) []string {
	var out []string
	fields := strings.Fields(use)
	for _, f := range fields[min(1, len(fields)):] { // field 0 is the command's own name
		if strings.HasPrefix(strings.TrimLeft(f, "[<"), "-") {
			break
		}
		name := strings.TrimRight(strings.Trim(f, "[]<>"), ".…")
		if entityPlaceholders[name] {
			out = append(out, f)
		}
	}
	return out
}
