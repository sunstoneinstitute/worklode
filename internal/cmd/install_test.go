package cmd

import (
	"encoding/json"
	"path/filepath"
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
	_, err := targetsFor(t, "--vcs", "svn")
	if err == nil {
		t.Fatal("--vcs svn was accepted, want an error")
	}
	if !strings.Contains(err.Error(), "unsupported --vcs") {
		t.Fatalf("error = %v, want it to mention \"unsupported --vcs\"", err)
	}
	_, err = targetsFor(t, "--agent", "emacs")
	if err == nil {
		t.Fatal("--agent emacs was accepted, want an error")
	}
	if !strings.Contains(err.Error(), "unsupported --agent") {
		t.Fatalf("error = %v, want it to mention \"unsupported --agent\"", err)
	}
}

func TestResolveHookTargetsRejectsContradictions(t *testing.T) {
	_, err := targetsFor(t, "--vcs", "git", "--no-vcs")
	if err == nil {
		t.Fatal("--vcs git --no-vcs was accepted, want an error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want it to mention \"mutually exclusive\"", err)
	}
	_, err = targetsFor(t, "--agent", "claude-code", "--no-agent")
	if err == nil {
		t.Fatal("--agent claude-code --no-agent was accepted, want an error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want it to mention \"mutually exclusive\"", err)
	}
}

// TestResolveHookTargetsOptOutsCrossTarget checks that opting out of one
// integration while explicitly (and non-contradictorily) naming the other does
// not trip the mutual-exclusion check — only naming *and* opting out of the
// *same* target is a contradiction.
func TestResolveHookTargetsOptOutsCrossTarget(t *testing.T) {
	got, err := targetsFor(t, "--no-vcs", "--agent", "claude-code")
	if err != nil {
		t.Fatalf("--no-vcs --agent claude-code: %v", err)
	}
	if got.vcs != "" || got.agent != agentClaudeCode {
		t.Fatalf("targets = %+v, want vcs empty, agent %q", got, agentClaudeCode)
	}
}

func TestResolveHookTargetsScopeDefault(t *testing.T) {
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	addHookFlags(cmd)
	if err := cmd.Flags().Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	scope, err := cmd.Flags().GetString("scope")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	if scope != scopeLocal {
		t.Fatalf("scope default = %q, want %q", scope, scopeLocal)
	}
}

func TestInstallUninstallCmdFlags(t *testing.T) {
	for _, tc := range []struct {
		cmd     *cobra.Command
		wantUse string
	}{
		{newInstallCmd(), "install"},
		{newUninstallCmd(), "uninstall"},
	} {
		if tc.cmd.Use != tc.wantUse {
			t.Errorf("Use = %q, want %q", tc.cmd.Use, tc.wantUse)
		}
		for _, name := range []string{"vcs", "agent", "no-vcs", "no-agent", "scope"} {
			if tc.cmd.Flags().Lookup(name) == nil {
				t.Errorf("%s: missing --%s flag", tc.wantUse, name)
			}
		}
	}
}

func TestInstallHooksBothIntegrations(t *testing.T) {
	root := initGitRepo(t)

	res, err := installHooks(root, hookTargets{vcs: vcsGit, agent: agentClaudeCode}, scopeLocal)
	if err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	if res.VCS == nil {
		t.Fatal("VCS result missing")
	}
	if !fileExists(filepath.Join(res.VCS.HooksDir, "pre-commit")) {
		t.Fatalf("pre-commit not written into %s", res.VCS.HooksDir)
	}
	if res.Agent == nil {
		t.Fatal("agent result missing")
	}
	if want := filepath.Join(root, ".claude", "settings.local.json"); res.Agent.Path != want {
		t.Fatalf("agent path = %q, want %q", res.Agent.Path, want)
	}
	settings := readSettings(t, res.Agent.Path)
	if got := commandsFor(t, settings, "SessionStart"); len(got) != 1 || got[0] != "lode hook session-start" {
		t.Fatalf("SessionStart commands: %v", got)
	}
}

func TestInstallHooksSkipsOptedOutIntegrations(t *testing.T) {
	root := initGitRepo(t)

	res, err := installHooks(root, hookTargets{vcs: vcsGit}, scopeLocal)
	if err != nil {
		t.Fatalf("installHooks --no-agent: %v", err)
	}
	if res.Agent != nil {
		t.Fatalf("agent result = %+v, want nil when the agent is skipped", res.Agent)
	}
	if fileExists(filepath.Join(root, ".claude", "settings.local.json")) {
		t.Fatal("--no-agent wrote a settings file")
	}

	root2 := initGitRepo(t)
	res2, err := installHooks(root2, hookTargets{agent: agentClaudeCode}, scopeLocal)
	if err != nil {
		t.Fatalf("installHooks --no-vcs: %v", err)
	}
	if res2.VCS != nil {
		t.Fatalf("VCS result = %+v, want nil when the VCS is skipped", res2.VCS)
	}
	hooksDir, err := resolveHooksDir(root2)
	if err != nil {
		t.Fatalf("resolveHooksDir: %v", err)
	}
	if fileExists(filepath.Join(hooksDir, "pre-commit")) {
		t.Fatal("--no-vcs wrote a pre-commit hook")
	}
}

func TestUninstallHooksUndoesInstall(t *testing.T) {
	root := initGitRepo(t)
	targets := hookTargets{vcs: vcsGit, agent: agentClaudeCode}
	if _, err := installHooks(root, targets, scopeLocal); err != nil {
		t.Fatalf("installHooks: %v", err)
	}

	res, err := uninstallHooks(root, targets, scopeLocal)
	if err != nil {
		t.Fatalf("uninstallHooks: %v", err)
	}
	if res.VCS == nil || res.VCS.Action != hookActionRemoved {
		t.Fatalf("VCS result = %+v, want action %q", res.VCS, hookActionRemoved)
	}
	if fileExists(filepath.Join(res.VCS.HooksDir, "pre-commit")) {
		t.Fatal("pre-commit still present after uninstall")
	}
	if res.Agent == nil {
		t.Fatal("agent result missing")
	}
	settings := readSettings(t, res.Agent.Path)
	if got := commandsFor(t, settings, "SessionStart"); len(got) != 0 {
		t.Fatalf("SessionStart after uninstall: %v, want none", got)
	}
}

func TestInstallCmdJSONOmitsSkippedIntegration(t *testing.T) {
	root := initGitRepo(t)
	res, err := installHooks(root, hookTargets{vcs: vcsGit}, scopeLocal)
	if err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := decoded["vcs"]; !ok {
		t.Fatalf("JSON missing the vcs key: %s", b)
	}
	if _, ok := decoded["agent"]; ok {
		t.Fatalf("JSON has an agent key for a skipped integration: %s", b)
	}
}
