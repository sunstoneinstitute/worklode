package cmd

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// leaves returns every leaf command under c.
func leaves(c *cobra.Command) []*cobra.Command {
	if !c.HasSubCommands() {
		return []*cobra.Command{c}
	}
	var out []*cobra.Command
	for _, s := range c.Commands() {
		out = append(out, leaves(s)...)
	}
	return out
}

// TestEveryLeafSaysWhereItRuns fails on a leaf command with no run-where
// annotation, so a new command cannot ship without deciding whether the MCP
// lode tool may run it (WL-REQ-1863).
func TestEveryLeafSaysWhereItRuns(t *testing.T) {
	for _, c := range leaves(newRootCmd()) {
		switch c.Annotations[annRunWhere] {
		case runRemoteVal, runLocalVal:
		default:
			t.Errorf("%q has no %q annotation: set runRemote or runLocal where the command is defined (WL-REQ-1863)", c.CommandPath(), annRunWhere)
		}
	}
}

// TestLocalCommandSet pins the list of local commands WL-REQ-1863 names, so
// a command cannot drift between local and remote unnoticed.
func TestLocalCommandSet(t *testing.T) {
	want := []string{
		"channel serve", "completion bash", "completion fish", "completion powershell", "completion zsh", "doc import", "doc lint", "doctor", "gate check", "graph derive",
		"install", "login", "logout", "next", "secret exec", "secret purge", "secret status",
		"skill install", "status", "task attach", "task claim", "uninstall",
		"work block", "work listen", "work next", "work resume", "work status", "work submit",
	}
	var got []string
	for _, c := range leaves(newRootCmd()) {
		if c.Annotations[annRunWhere] == runLocalVal {
			_, p, _ := strings.Cut(c.CommandPath(), " ")
			got = append(got, p)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("local commands:\n got %v\nwant %v", got, want)
	}
}

func TestRemoteRefusal(t *testing.T) {
	cases := []struct {
		name    string
		argv    []string // command path then args and flags
		refused string   // expected path in the error, empty when allowed
	}{
		{"remote command", []string{"task", "list"}, ""},
		{"shortcut board", []string{"board"}, ""},
		{"local command", []string{"work", "next"}, "work next"},
		{"local shortcut", []string{"next"}, "next"},
		{"local top level", []string{"install"}, "install"},
		{"secret catalog is remote", []string{"secret", "catalog"}, ""},
		{"secret exec is local", []string{"secret", "exec", "--", "true"}, "secret exec"},
		{"claim without worktree", []string{"task", "claim", "WL-1"}, "task claim"},
		{"claim with worktree", []string{"task", "claim", "WL-1", "--worktree", "w1"}, ""},
		{"claim with empty worktree", []string{"task", "claim", "WL-1", "--worktree", ""}, "task claim"},
		{"lint stdin", []string{"doc", "lint", "-"}, ""},
		{"lint file", []string{"doc", "lint", "file.md"}, "doc lint"},
		{"lint nothing", []string{"doc", "lint"}, "doc lint"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCmd()
			c, args, err := root.Find(tc.argv)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.ParseFlags(args); err != nil {
				t.Fatal(err)
			}
			err = remoteRefusal(c, c.Flags().Args())
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			var lce *LocalCommandError
			if !errors.As(err, &lce) {
				t.Fatalf("err = %v, want *LocalCommandError", err)
			}
			if lce.Path != tc.refused {
				t.Errorf("path = %q, want %q", lce.Path, tc.refused)
			}
			if !strings.Contains(err.Error(), "lode "+tc.refused) || !strings.Contains(err.Error(), "needs the lode binary on the caller's machine") {
				t.Errorf("message %q does not name the command and the binary", err)
			}
		})
	}
}
