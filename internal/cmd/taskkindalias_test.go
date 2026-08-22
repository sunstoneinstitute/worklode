package cmd

import (
	"bytes"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// TestWarnDeprecatedTaskKind is a pure unit test of the warning text: no
// server, no Postgres involved, so it runs everywhere.
func TestWarnDeprecatedTaskKind(t *testing.T) {
	tests := map[string]string{
		"spec":   `warning: task kind "spec" is deprecated, use "design"` + "\n",
		"design": "",
		"bug":    "",
		"":       "",
	}
	for kind, want := range tests {
		t.Run(kind, func(t *testing.T) {
			var stderr bytes.Buffer
			cmd := &cobra.Command{Use: "x"}
			cmd.SetErr(&stderr)
			warnDeprecatedTaskKind(cmd, kind)
			if got := stderr.String(); got != want {
				t.Errorf("warnDeprecatedTaskKind(%q): stderr = %q, want %q", kind, got, want)
			}
		})
	}
}

// taskKindCommands pins the command paths whose --kind flag takes a task
// kind (ns.TaskKinds) rather than a document kind or anything else spelled
// "kind". Each one must call warnDeprecatedTaskKind from its RunE.
// TestTaskKindCommandsArePinned checks this list against a live walk of the
// cobra tree, so a new command that adds a task-kind --kind flag without
// updating this list fails loudly instead of silently skipping the warning.
var taskKindCommands = []string{
	"lode inbox promote",
	"lode next",
	"lode task add",
	"lode task claim",
	"lode task edit",
	"lode task list",
}

// TestTaskKindCommandsArePinned walks the full cobra tree for every command
// whose --kind usage enumerates ns.TaskKinds — the same identification
// TestKindEnumMatchesNS pins for `task add` alone — and asserts the set
// matches taskKindCommands exactly. It cannot see whether
// warnDeprecatedTaskKind is actually wired into a command's RunE without
// running the command, so the contract is: taskKindCommands is manually
// verified to be wired today, and any diff this test reports is the signal
// that the new or removed command needs the same wiring, not that the test
// itself checked it.
func TestTaskKindCommandsArePinned(t *testing.T) {
	var got []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if f := lookupFlag(c, "kind"); f != nil {
			vals := slices.Clone(enumValues(f))
			sort.Strings(vals)
			if slices.Equal(vals, ns.TaskKinds) {
				got = append(got, c.CommandPath())
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
	sort.Strings(got)

	want := slices.Clone(taskKindCommands)
	sort.Strings(want)

	if !slices.Equal(got, want) {
		t.Errorf("commands whose --kind enumerates ns.TaskKinds = %v, want %v\n"+
			"a command with a task-kind --kind flag was added or removed: if added, call "+
			"warnDeprecatedTaskKind from its RunE (see internal/cmd/taskkindalias.go) and add "+
			"its command path to taskKindCommands in this file", got, want)
	}
}

// TestTaskAddWarnsOnDeprecatedKind proves `lode task add --kind spec` still
// succeeds (the server normalises it to design), prints the exact
// deprecation warning on stderr, and prints nothing about it on stdout.
func TestTaskAddWarnsOnDeprecatedKind(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	stdout, stderr, err := runLodeOutErr(t, "task", "add", "--project", "proj",
		"--title", "Spec task", "--kind", "spec")
	if err != nil {
		t.Fatalf("task add --kind spec: %v\nstderr: %s", err, stderr)
	}
	const want = `warning: task kind "spec" is deprecated, use "design"` + "\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if strings.Contains(stdout, "deprecated") {
		t.Errorf("stdout unexpectedly mentions the deprecation warning: %q", stdout)
	}
}

// TestTaskAddNoWarningOnCurrentKind proves the current kind name prints no
// warning on either stream.
func TestTaskAddNoWarningOnCurrentKind(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	_, stderr, err := runLodeOutErr(t, "task", "add", "--project", "proj",
		"--title", "Design task", "--kind", "design")
	if err != nil {
		t.Fatalf("task add --kind design: %v\nstderr: %s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}
