package cmd

import (
	"bytes"
	"encoding/json"
	"os"
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

// TestInstallHooksJSONOmitsSkippedIntegration checks the installResult struct
// tag directly: json.Marshal on a skipped integration's nil field must omit
// the key entirely. This proves the struct is shaped right, not that the
// command actually emits it — see TestInstallCmdJSONOmitsSkippedIntegration
// below for the real end-to-end check.
func TestInstallHooksJSONOmitsSkippedIntegration(t *testing.T) {
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

// --- ISSUE 1: partial failure still reports what already landed -----------

// writeCorruptSettings seeds root's local Claude settings file with invalid
// JSON, so installClaudeHooks/uninstallClaudeHooks fail on the agent step
// while leaving any prior VCS step's result standing.
func writeCorruptSettings(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "settings.local.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("seed corrupt settings: %v", err)
	}
}

func TestInstallHooksReturnsPartialResultOnAgentFailure(t *testing.T) {
	root := initGitRepo(t)
	writeCorruptSettings(t, root)

	res, err := installHooks(root, hookTargets{vcs: vcsGit, agent: agentClaudeCode}, scopeLocal)
	if err == nil {
		t.Fatal("installHooks with corrupt settings: err = nil, want a parse error")
	}
	if res.VCS == nil {
		t.Fatal("partial result missing VCS: the git hook step already succeeded")
	}
	if !fileExists(filepath.Join(res.VCS.HooksDir, "pre-commit")) {
		t.Fatal("pre-commit not written despite the VCS step succeeding")
	}
	if res.Agent != nil {
		t.Fatalf("agent result = %+v, want nil: the agent step failed", res.Agent)
	}
}

func TestUninstallHooksReturnsPartialResultOnAgentFailure(t *testing.T) {
	root := initGitRepo(t)
	targets := hookTargets{vcs: vcsGit, agent: agentClaudeCode}
	if _, err := installHooks(root, targets, scopeLocal); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	writeCorruptSettings(t, root)

	res, err := uninstallHooks(root, targets, scopeLocal)
	if err == nil {
		t.Fatal("uninstallHooks with corrupt settings: err = nil, want a parse error")
	}
	if res.VCS == nil || res.VCS.Action != hookActionRemoved {
		t.Fatalf("partial result VCS = %+v, want action %q: the git hook step already succeeded", res.VCS, hookActionRemoved)
	}
	if fileExists(filepath.Join(res.VCS.HooksDir, "pre-commit")) {
		t.Fatal("pre-commit still present: the VCS uninstall step should have removed it")
	}
	if res.Agent != nil {
		t.Fatalf("agent result = %+v, want nil: the agent step failed", res.Agent)
	}
}

func TestInstallCmdReportsPartialResultBeforeFailing(t *testing.T) {
	root := initGitRepo(t)
	writeCorruptSettings(t, root)
	t.Chdir(root)

	out, err := runLode(t, "install")
	if err == nil {
		t.Fatal("install with corrupt settings: err = nil, want a parse error")
	}
	hooksDir, herr := resolveHooksDir(root)
	if herr != nil {
		t.Fatalf("resolveHooksDir: %v", herr)
	}
	if !fileExists(filepath.Join(hooksDir, "pre-commit")) {
		t.Fatal("pre-commit was not installed despite the VCS step succeeding")
	}
	if !strings.Contains(out, "git: installed pre-commit hook in "+hooksDir) {
		t.Fatalf("output missing the partial VCS report before the failure: %q", out)
	}
}

func TestUninstallCmdReportsPartialResultBeforeFailing(t *testing.T) {
	root := initGitRepo(t)
	if _, err := installHooks(root, hookTargets{vcs: vcsGit, agent: agentClaudeCode}, scopeLocal); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	writeCorruptSettings(t, root)
	t.Chdir(root)

	out, err := runLode(t, "uninstall")
	if err == nil {
		t.Fatal("uninstall with corrupt settings: err = nil, want a parse error")
	}
	hooksDir, herr := resolveHooksDir(root)
	if herr != nil {
		t.Fatalf("resolveHooksDir: %v", herr)
	}
	if fileExists(filepath.Join(hooksDir, "pre-commit")) {
		t.Fatal("pre-commit still present: the VCS uninstall step should have removed it despite the later agent failure")
	}
	if !strings.Contains(out, "git: removed pre-commit hook from "+hooksDir) {
		t.Fatalf("output missing the partial VCS report before the failure: %q", out)
	}
}

// --- ISSUE 5: an unrecognized uninstall action is reported honestly --------

func TestReportUninstallUnknownVCSActionIsHonest(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	res := uninstallResult{VCS: &vcsUninstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks", Action: "bogus"}}
	if err := reportUninstall(cmd, res); err != nil {
		t.Fatalf("reportUninstall: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"bogus"`) || !strings.Contains(got, "unexpected") {
		t.Fatalf("output = %q, want it to name the unexpected action honestly", got)
	}
	if strings.Contains(got, "no Worklode pre-commit hook") {
		t.Fatalf("output = %q, an unknown action must not be reported as the no-op message", got)
	}
}

// --- ISSUE 2: uninstall no longer claims to remove hooks that were never there --

// TestUninstallCmdReportsHonestNoOpForBothIntegrations reproduces the
// reviewer's repro directly: `lode uninstall` in a repo with nothing
// installed must report a genuine no-op on both integrations, not "removed"
// for a settings file that was never written.
func TestUninstallCmdReportsHonestNoOpForBothIntegrations(t *testing.T) {
	root := initGitRepo(t)
	t.Chdir(root)

	out, err := runLode(t, "uninstall")
	if err != nil {
		t.Fatalf("uninstall on a repo with nothing installed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "git: no Worklode pre-commit hook in") {
		t.Fatalf("output missing the VCS no-op line: %q", out)
	}
	if !strings.Contains(out, "claude-code: no Worklode hooks in") {
		t.Fatalf("output missing the agent no-op line: %q", out)
	}
	if strings.Contains(out, "removed") {
		t.Fatalf("output claims something was removed on a repo with nothing installed: %q", out)
	}
	settingsPath := filepath.Join(root, ".claude", "settings.local.json")
	if fileExists(settingsPath) {
		t.Fatalf("uninstall created a settings file at %s that never existed", settingsPath)
	}
}

// --- ISSUE 3: reportInstall/reportUninstall coverage ------------------------

func TestReportInstallLinesWithChainTarget(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	res := installResult{
		VCS:   &vcsInstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks", ChainedTo: "/repo/.git/hooks/pre-commit.pre-lode"},
		Agent: &agentInstall{Agent: agentClaudeCode, Path: "/repo/.claude/settings.local.json"},
	}
	if err := reportInstall(cmd, res); err != nil {
		t.Fatalf("reportInstall: %v", err)
	}
	want := "git: installed pre-commit hook in /repo/.git/hooks\n" +
		"git: chains to /repo/.git/hooks/pre-commit.pre-lode\n" +
		"claude-code: installed hooks in /repo/.claude/settings.local.json\n"
	if buf.String() != want {
		t.Fatalf("output = %q, want %q", buf.String(), want)
	}
}

func TestReportInstallNoChainLineWhenNothingToChainTo(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	res := installResult{VCS: &vcsInstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks"}}
	if err := reportInstall(cmd, res); err != nil {
		t.Fatalf("reportInstall: %v", err)
	}
	want := "git: installed pre-commit hook in /repo/.git/hooks\n"
	if buf.String() != want {
		t.Fatalf("output = %q, want %q (no \"chains to\" line)", buf.String(), want)
	}
}

func TestReportUninstallVCSActions(t *testing.T) {
	for _, tc := range []struct {
		name, action, want string
	}{
		{"removed", hookActionRemoved, "git: removed pre-commit hook from /repo/.git/hooks\n"},
		{"restored", hookActionRestored, "git: removed pre-commit hook from /repo/.git/hooks and restored the previous one\n"},
		{"none", hookActionNone, "git: no Worklode pre-commit hook in /repo/.git/hooks\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			res := uninstallResult{VCS: &vcsUninstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks", Action: tc.action}}
			if err := reportUninstall(cmd, res); err != nil {
				t.Fatalf("reportUninstall: %v", err)
			}
			if buf.String() != tc.want {
				t.Fatalf("output = %q, want %q", buf.String(), tc.want)
			}
		})
	}
}

func TestReportUninstallAgentActions(t *testing.T) {
	for _, tc := range []struct {
		name, action, want string
	}{
		{"removed", hookActionRemoved, "claude-code: removed hooks from /repo/.claude/settings.local.json\n"},
		{"none", hookActionNone, "claude-code: no Worklode hooks in /repo/.claude/settings.local.json\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			res := uninstallResult{Agent: &agentUninstall{Agent: agentClaudeCode, Path: "/repo/.claude/settings.local.json", Action: tc.action}}
			if err := reportUninstall(cmd, res); err != nil {
				t.Fatalf("reportUninstall: %v", err)
			}
			if buf.String() != tc.want {
				t.Fatalf("output = %q, want %q", buf.String(), tc.want)
			}
		})
	}
}

// jsonCmd builds a bare *cobra.Command carrying a local --json flag (the real
// commands rely on the persistent one declared on rootCmd, which a standalone
// test command doesn't have), set to true, with output captured in buf.
func jsonCmd(buf *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Set("json", "true")
	cmd.SetOut(buf)
	return cmd
}

func TestReportInstallJSON(t *testing.T) {
	var buf bytes.Buffer
	cmd := jsonCmd(&buf)
	res := installResult{VCS: &vcsInstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks"}}
	if err := reportInstall(cmd, res); err != nil {
		t.Fatalf("reportInstall: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s: %v", buf.String(), err)
	}
	if _, ok := decoded["vcs"]; !ok {
		t.Fatalf("JSON missing vcs key: %s", buf.String())
	}
	if _, ok := decoded["agent"]; ok {
		t.Fatalf("JSON has an agent key for a skipped integration: %s", buf.String())
	}
}

func TestReportUninstallJSON(t *testing.T) {
	var buf bytes.Buffer
	cmd := jsonCmd(&buf)
	res := uninstallResult{
		VCS:   &vcsUninstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks", Action: hookActionRemoved},
		Agent: &agentUninstall{Agent: agentClaudeCode, Path: "/repo/.claude/settings.local.json", Action: hookActionNone},
	}
	if err := reportUninstall(cmd, res); err != nil {
		t.Fatalf("reportUninstall: %v", err)
	}
	var decoded struct {
		VCS   struct{ Action string } `json:"vcs"`
		Agent struct{ Action string } `json:"agent"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s: %v", buf.String(), err)
	}
	if decoded.VCS.Action != hookActionRemoved {
		t.Fatalf("vcs.action = %q, want %q", decoded.VCS.Action, hookActionRemoved)
	}
	if decoded.Agent.Action != hookActionNone {
		t.Fatalf("agent.action = %q, want %q", decoded.Agent.Action, hookActionNone)
	}
}

// --- ISSUE 4: real end-to-end --json coverage --------------------------------

// TestInstallCmdJSONOmitsSkippedIntegration runs the actual install command
// (not just the installResult struct) and decodes its stdout, so a bug in
// reportInstall's JSON path — not just the struct tags — would fail this.
func TestInstallCmdJSONOmitsSkippedIntegration(t *testing.T) {
	root := initGitRepo(t)
	t.Chdir(root)

	var buf bytes.Buffer
	cmd := newInstallCmd()
	cmd.Flags().Bool("json", false, "")
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--json", "--no-agent"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute install --json --no-agent: %v\noutput: %s", err, buf.String())
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s: %v", buf.String(), err)
	}
	if _, ok := decoded["vcs"]; !ok {
		t.Fatalf("JSON missing the vcs key: %s", buf.String())
	}
	if _, ok := decoded["agent"]; ok {
		t.Fatalf("JSON has an agent key for a skipped integration: %s", buf.String())
	}
}

// TestUninstallCmdJSONIncludesActionOnBothSides mirrors the install e2e test
// for uninstall: both integrations must carry an "action" key in real --json
// output, not just in the struct.
func TestUninstallCmdJSONIncludesActionOnBothSides(t *testing.T) {
	root := initGitRepo(t)
	if _, err := installHooks(root, hookTargets{vcs: vcsGit, agent: agentClaudeCode}, scopeLocal); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	t.Chdir(root)

	var buf bytes.Buffer
	cmd := newUninstallCmd()
	cmd.Flags().Bool("json", false, "")
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute uninstall --json: %v\noutput: %s", err, buf.String())
	}

	var decoded struct {
		VCS   struct{ Action string } `json:"vcs"`
		Agent struct{ Action string } `json:"agent"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s: %v", buf.String(), err)
	}
	if decoded.VCS.Action != hookActionRemoved {
		t.Fatalf("vcs.action = %q, want %q", decoded.VCS.Action, hookActionRemoved)
	}
	if decoded.Agent.Action != hookActionRemoved {
		t.Fatalf("agent.action = %q, want %q", decoded.Agent.Action, hookActionRemoved)
	}
}
