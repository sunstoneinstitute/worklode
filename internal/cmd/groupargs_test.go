package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestGroupRejectsUnknownSubcommand pins the fix for WL-480. cobra's
// legacyArgs() errors on an unknown first argument only for the root command;
// every other parent accepts anything and, being non-runnable, falls through
// to printing help and exiting 0. That made a renamed subcommand fail
// silently for out-of-tree callers — `lode task ready $ID && echo done`
// printed "done" and published nothing.
func TestGroupRejectsUnknownSubcommand(t *testing.T) {
	groupTopLevel()
	rejectStrayGroupArgs(rootCmd)
	for _, args := range [][]string{
		{"task", "bogusnothing"},
		{"doc", "bogusnothing"},
		{"project", "bogusnothing"},
		{"work", "bogusnothing"},
		{"graph", "projection", "bogusnothing"},
	} {
		path := strings.Join(args, " ")
		out, err := runLode(t, args...)
		if err == nil {
			t.Errorf("lode %s: want an error, got nil (output: %q)", path, out)
			continue
		}
		if !strings.Contains(err.Error(), "bogusnothing") {
			t.Errorf("lode %s: error %q does not name the unknown subcommand", path, err)
		}
	}
}

// TestGroupAcceptsBareInvocation guards the other direction: `lode task` with
// no arguments is a help request, not an error.
func TestGroupAcceptsBareInvocation(t *testing.T) {
	groupTopLevel()
	rejectStrayGroupArgs(rootCmd)
	for _, name := range []string{"task", "doc", "project", "work"} {
		if _, err := runLode(t, name); err != nil {
			t.Errorf("lode %s: want help and no error, got %v", name, err)
		}
	}
}

// TestEveryGroupRejectsStrayArgs is the tripwire, and it is behavioural on
// purpose. A structural check ("every group sets Args") goes vacuous the
// moment rejectStrayGroupArgs runs, since it sets Args on exactly the
// commands such a check would inspect. This drives every parent in the built
// tree and asserts the error reaches the caller, so a group that stops
// rejecting a stray argument fails here whatever the mechanism — and it does
// not care whether an earlier test already applied the fix.
func TestEveryGroupRejectsStrayArgs(t *testing.T) {
	groupTopLevel()
	rootCmd.InitDefaultCompletionCmd()
	rejectStrayGroupArgs(rootCmd)

	var checked int
	var walk func(*cobra.Command, []string)
	walk = func(c *cobra.Command, path []string) {
		for _, sub := range c.Commands() {
			walk(sub, append(append([]string{}, path...), sub.Name()))
		}
		if !c.HasSubCommands() || len(path) == 0 {
			return
		}
		// A parent that genuinely takes a positional argument is not a group
		// in this sense: `lode project crew <project>` has subcommands and
		// consumes an argument of its own. Ask its own validator rather than
		// hardcoding the exceptions.
		if c.Args != nil && c.Args(c, []string{"bogusnothing"}) == nil {
			return
		}
		checked++
		name := "lode " + strings.Join(path, " ") + " bogusnothing"
		out, err := runLode(t, append(append([]string{}, path...), "bogusnothing")...)
		if err == nil {
			t.Errorf("%s: want an error, got nil (printed %d bytes of help)", name, len(out))
			return
		}
		if !strings.Contains(err.Error(), "bogusnothing") {
			t.Errorf("%s: error %q does not name the unknown subcommand", name, err)
		}
	}
	walk(rootCmd, nil)

	if checked < 15 {
		t.Fatalf("only %d groups checked; the walk is not reaching the tree", checked)
	}
}

// TestRunnableParentsAreLeftAlone pins the exclusion above. `lode project
// crew <project>` has subcommands and takes a real positional argument;
// forcing NoArgs onto it would break a working command.
func TestRunnableParentsAreLeftAlone(t *testing.T) {
	groupTopLevel()
	rejectStrayGroupArgs(rootCmd)

	crew, _, err := rootCmd.Find([]string{"project", "crew"})
	if err != nil {
		t.Fatalf("find project crew: %v", err)
	}
	if crew.Args == nil {
		t.Fatal("project crew has no Args validator")
	}
	if err := crew.Args(crew, []string{"someproject"}); err != nil {
		t.Errorf("project crew rejected its own positional argument: %v", err)
	}
}
