package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// targetsFor builds a bare command carrying the shared install/uninstall flags,
// parses args against it, and resolves them — the same path both real commands
// take, without running anything.
func targetsFor(t *testing.T, args ...string) (hookTargets, error) {
	t.Helper()
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	addHookFlags(cmd)
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return resolveHookTargets(cmd)
}

func TestResolveHookTargetsDefaults(t *testing.T) {
	got, err := targetsFor(t)
	if err != nil {
		t.Fatalf("resolveHookTargets: %v", err)
	}
	if got.vcs != vcsGit || got.agent != agentClaudeCode {
		t.Fatalf("targets = %+v, want vcs=%q agent=%q", got, vcsGit, agentClaudeCode)
	}
}

func TestResolveHookTargetsOptOuts(t *testing.T) {
	got, err := targetsFor(t, "--no-vcs")
	if err != nil {
		t.Fatalf("--no-vcs: %v", err)
	}
	if got.vcs != "" || got.agent != agentClaudeCode {
		t.Fatalf("--no-vcs targets = %+v, want vcs empty, agent %q", got, agentClaudeCode)
	}

	got, err = targetsFor(t, "--no-agent")
	if err != nil {
		t.Fatalf("--no-agent: %v", err)
	}
	if got.vcs != vcsGit || got.agent != "" {
		t.Fatalf("--no-agent targets = %+v, want vcs %q, agent empty", got, vcsGit)
	}
}

func TestResolveHookTargetsRejectsNothingToDo(t *testing.T) {
	_, err := targetsFor(t, "--no-vcs", "--no-agent")
	if err == nil {
		t.Fatal("--no-vcs --no-agent was accepted, want an error")
	}
	if !strings.Contains(err.Error(), "nothing to do") {
		t.Fatalf("error = %v, want it to mention \"nothing to do\"", err)
	}
}

func TestResolveHookTargetsRejectsUnsupportedNames(t *testing.T) {
	if _, err := targetsFor(t, "--vcs", "svn"); err == nil {
		t.Fatal("--vcs svn was accepted, want an error")
	}
	if _, err := targetsFor(t, "--agent", "emacs"); err == nil {
		t.Fatal("--agent emacs was accepted, want an error")
	}
}

func TestResolveHookTargetsRejectsContradictions(t *testing.T) {
	if _, err := targetsFor(t, "--vcs", "git", "--no-vcs"); err == nil {
		t.Fatal("--vcs git --no-vcs was accepted, want an error")
	}
	if _, err := targetsFor(t, "--agent", "claude-code", "--no-agent"); err == nil {
		t.Fatal("--agent claude-code --no-agent was accepted, want an error")
	}
}
