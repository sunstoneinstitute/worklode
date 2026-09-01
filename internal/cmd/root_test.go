package cmd

import (
	"strings"
	"testing"
)

// TestShortcuts holds 061 §1 L9: the top-level aliases are exactly the ones
// the `shortcuts` table names, each one resolving to a real nested command,
// and the table is the only place a top-level alias is declared.
func TestShortcuts(t *testing.T) {
	groupTopLevel()

	if len(shortcuts) > 4 {
		t.Errorf("061 §1 L9 closes the shortcut list at four; the table has %d. "+
			"Adding a fifth requires amending L9.", len(shortcuts))
	}

	for _, s := range shortcuts {
		path := strings.Join(s.target, " ")
		if len(s.target) < 2 {
			t.Errorf("shortcut %q: a shortcut aliases a nested command", path)
			continue
		}
		if s.reason == "" {
			t.Errorf("shortcut %q: needs the L9 reason it earns a shortcut", path)
		}

		// The target is a real, runnable command.
		target, rest := resolve(rootCmd, s.target)
		if len(rest) > 0 || target == rootCmd {
			t.Errorf("shortcut %q: `lode %s` is not a command", path, path)
			continue
		}
		if !target.Runnable() {
			t.Errorf("shortcut %q: `lode %s` is not runnable", path, path)
		}

		// And it is registered at the root, under the shortcuts heading.
		name := s.target[len(s.target)-1]
		top := findSub(rootCmd, name)
		if top == nil {
			t.Errorf("shortcut %q: `lode %s` is not registered at the root", path, name)
			continue
		}
		if top.GroupID != shortcutGroupID {
			t.Errorf("shortcut %q: `lode %s` is not in the shortcuts help group", path, name)
		}
		if top.Short != target.Short {
			t.Errorf("shortcut %q: `lode %s` and `lode %s` describe themselves differently",
				path, name, path)
		}
	}

	// No top-level alias outside the table. Cobra's Aliases is how `lode
	// ready` got in unnoticed; the table is meant to be the visible closed
	// set instead.
	for _, c := range rootCmd.Commands() {
		if len(c.Aliases) > 0 {
			t.Errorf("`lode %s` declares Aliases %v: top-level aliases belong in the "+
				"shortcuts table in root.go (061 §1 L9)", c.Name(), c.Aliases)
		}
	}
}

// TestHelpGroups checks `lode --help` puts the shortcuts under their own
// heading and leaves everything else under the ordinary one, rather than
// cobra's "Additional Commands:" fallback.
func TestHelpGroups(t *testing.T) {
	groupTopLevel()

	var out strings.Builder
	rootCmd.SetOut(&out)
	defer rootCmd.SetOut(nil)
	if err := rootCmd.Help(); err != nil {
		t.Fatalf("lode --help: %v", err)
	}
	help := out.String()

	for _, want := range []string{"Available Commands:", "Shortcuts:\n  board"} {
		if !strings.Contains(help, want) {
			t.Errorf("lode --help is missing %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "Additional Commands:") {
		t.Errorf("lode --help has ungrouped top-level commands:\n%s", help)
	}
	// A shortcut is listed once, under its own heading.
	if n := strings.Count(help, "\n  board "); n != 1 {
		t.Errorf("`board` appears %d times in lode --help, want 1:\n%s", n, help)
	}
}
