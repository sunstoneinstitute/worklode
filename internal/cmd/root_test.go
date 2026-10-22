package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestShortcuts holds WL-REQ-305 L9: the top-level aliases are exactly the ones
// the `shortcuts` table names, each one resolving to a real nested command,
// and the table is the only place a top-level alias is declared.
func TestShortcuts(t *testing.T) {
	root := newRootCmd()

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
		target, rest := resolve(root, s.target)
		if len(rest) > 0 || target == root {
			t.Errorf("shortcut %q: `lode %s` is not a command", path, path)
			continue
		}
		if !target.Runnable() {
			t.Errorf("shortcut %q: `lode %s` is not runnable", path, path)
		}

		// And it is registered at the root, under the shortcuts heading.
		name := s.target[len(s.target)-1]
		top := findSub(root, name)
		if top == nil {
			t.Errorf("shortcut %q: `lode %s` is not registered at the root", path, name)
			continue
		}
		if top.GroupID != shortcutGroupID {
			t.Errorf("shortcut %q: `lode %s` is not in the shortcuts help group", path, name)
		}
		if s.argTarget != nil {
			at, rest := resolve(root, s.argTarget)
			if at == root || len(rest) > 1 || (len(rest) == 1 && at.Flags().Lookup(strings.TrimPrefix(rest[0], "--")) == nil) {
				t.Errorf("shortcut %q: argTarget `lode %s` is not a command", path, strings.Join(s.argTarget, " "))
			}
		}
		if top.Short != target.Short {
			t.Errorf("shortcut %q: `lode %s` and `lode %s` describe themselves differently",
				path, name, path)
		}
	}

	// No top-level alias outside the table. Cobra's Aliases is how `lode
	// ready` got in unnoticed; the table is meant to be the visible closed
	// set instead.
	for _, c := range root.Commands() {
		if isCobraGenerated(c.Name()) {
			continue
		}
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
	root := newRootCmd()

	var out strings.Builder
	root.SetOut(&out)
	if err := root.Help(); err != nil {
		t.Fatalf("lode --help: %v", err)
	}
	help := out.String()

	for _, want := range []string{"Available Commands:", "Shortcuts:\n  board", "\n  next", "\n  status"} {
		if !strings.Contains(help, want) {
			t.Errorf("lode --help is missing %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "Additional Commands:") {
		t.Errorf("lode --help has ungrouped top-level commands:\n%s", help)
	}
	// Each shortcut is listed once, under its own heading.
	for _, name := range []string{"board", "next", "status"} {
		if n := strings.Count(help, "\n  "+name+" "); n != 1 {
			t.Errorf("%q appears %d times in lode --help, want 1:\n%s", name, n, help)
		}
	}
}

// TestRootCmdRunsTwiceInOneProcess holds WL-REQ-1862: newRootCmd builds a
// fresh tree per call, so one run's flags never reach the next, and two runs
// at once share nothing a -race build would flag. `doc lint <file>` runs
// offline, and --json visibly changes its output, which makes a leaked flag
// observable.
func TestRootCmdRunsTwiceInOneProcess(t *testing.T) {
	path := writeDocFile(t, docTestBody)

	// lintOut runs `lode doc lint <path>`, with --json when asJSON is set, on
	// its own tree, and reports whether the output was the JSON report.
	lintOut := func(asJSON bool) (gotJSON bool, out string) {
		args := []string{"doc", "lint", path}
		if asJSON {
			args = append(args, "--json")
		}
		root := newRootCmd()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs(args)
		_ = root.Execute() // findings return an error; the output is what counts
		out = buf.String()
		var rep docLintFileReport
		return json.Unmarshal([]byte(out), &rep) == nil && rep.File == path, out
	}

	// Sequential: --json on the first run must not survive into the second.
	if gotJSON, out := lintOut(true); !gotJSON {
		t.Fatalf("first run with --json printed no JSON report:\n%s", out)
	}
	if gotJSON, out := lintOut(false); gotJSON {
		t.Fatalf("second run without --json printed the JSON report; the first run's flag leaked:\n%s", out)
	}

	// Concurrent: runs with and without --json at once, each on its own tree.
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for i := range 16 {
		wg.Add(1)
		go func(asJSON bool) {
			defer wg.Done()
			if gotJSON, out := lintOut(asJSON); gotJSON != asJSON {
				errs <- fmt.Sprintf("run with --json=%v printed JSON=%v:\n%s", asJSON, gotJSON, out)
			}
		}(i%2 == 0)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// TestRootCmdBuildsShareNoCommand guards against a command or flag held in a
// package-level variable and attached to every tree: two builds must not
// share a single *cobra.Command or *pflag.Flag.
func TestRootCmdBuildsShareNoCommand(t *testing.T) {
	cmds := map[*cobra.Command]string{}
	flags := map[*pflag.Flag]string{}
	var collect func(c *cobra.Command, record bool)
	collect = func(c *cobra.Command, record bool) {
		check := func(f *pflag.Flag) {
			if record {
				flags[f] = c.CommandPath()
			} else if p, ok := flags[f]; ok {
				t.Errorf("--%s on %q is the same *pflag.Flag in two builds (first seen on %q)", f.Name, c.CommandPath(), p)
			}
		}
		if record {
			cmds[c] = c.CommandPath()
		} else if _, ok := cmds[c]; ok {
			t.Errorf("%q is the same *cobra.Command in two builds", c.CommandPath())
		}
		c.LocalFlags().VisitAll(check)
		for _, sub := range c.Commands() {
			collect(sub, record)
		}
	}
	collect(newRootCmd(), true)
	collect(newRootCmd(), false)
	if len(cmds) < 100 {
		t.Fatalf("only %d commands walked; the tree is not being reached", len(cmds))
	}
}
