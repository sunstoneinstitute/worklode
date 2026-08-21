package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/githooks"
	"github.com/sunstoneinstitute/worklode/internal/harness"
	"github.com/sunstoneinstitute/worklode/internal/skillhash"
	"github.com/sunstoneinstitute/worklode/internal/skillstore"
)

// readSettings reads a settings file through the same reader the adapter
// uses, failing the test if it is malformed. A missing file reads as empty
// settings, as it does for an install.
func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	settings, err := harness.ReadJSONFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return settings
}

// discardCmd is a throwaway command supplying the streams installHooks writes
// warnings to, for tests that do not inspect them. Tests that do assert on a
// warning build their own command with SetErr.
func discardCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd
}

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

// claudeCode is the adapter id the install tests exercise. It is a literal,
// not a const in install.go: the CLI resolves agents through the registry.
const claudeCode = "claude-code"

// claudeTargets is the explicit single-agent target these tests use, so they
// never depend on whether the machine running them happens to have a ~/.claude.
func claudeTargets(vcs string, statusLine bool) hookTargets {
	return hookTargets{vcs: vcs, agents: []string{claudeCode}, statusLine: statusLine}
}

func TestResolveHookTargetsDefaults(t *testing.T) {
	got, err := targetsFor(t)
	if err != nil {
		t.Fatalf("resolveHookTargets: %v", err)
	}
	// auto is not resolved at flag time: there is no repo directory yet.
	if got.vcs != vcsGit || len(got.agents) != 1 || got.agents[0] != "auto" {
		t.Fatalf("targets = %+v, want vcs=%q agents=[auto]", got, vcsGit)
	}
}

func TestResolveHookTargetsRepeatableAgent(t *testing.T) {
	got, err := targetsFor(t, "--agent", claudeCode)
	if err != nil {
		t.Fatalf("one agent: %v", err)
	}
	if len(got.agents) != 1 || got.agents[0] != claudeCode {
		t.Fatalf("agents = %v", got.agents)
	}
	// Duplicates collapse, preserving the order given.
	got, err = targetsFor(t, "--agent", claudeCode, "--agent", claudeCode)
	if err != nil || len(got.agents) != 1 || got.agents[0] != claudeCode {
		t.Fatalf("dedupe: %v %v", got.agents, err)
	}
}

func TestResolveHookTargetsAgentAll(t *testing.T) {
	got, err := targetsFor(t, "--agent", "all")
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(got.agents) != len(harness.IDs()) {
		t.Fatalf("all = %v; want every registered id %v", got.agents, harness.IDs())
	}
	for i, id := range harness.IDs() {
		if got.agents[i] != id {
			t.Fatalf("all = %v, want %v", got.agents, harness.IDs())
		}
	}
	// A pseudo-id alongside an explicit one contradicts the intent of either.
	if _, err := targetsFor(t, "--agent", "all", "--agent", claudeCode); err == nil {
		t.Fatal("all + explicit id accepted")
	}
	if _, err := targetsFor(t, "--agent", "auto", "--agent", claudeCode); err == nil {
		t.Fatal("auto + explicit id accepted")
	}
}

func TestResolveHookTargetsRejectsUnknownAgent(t *testing.T) {
	_, err := targetsFor(t, "--agent", "opencode")
	if err == nil {
		t.Fatal("--agent opencode was accepted, want an error")
	}
	for _, id := range harness.IDs() {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("unknown agent error must list supported ids (%v), got %v", harness.IDs(), err)
		}
	}
	if !strings.Contains(err.Error(), "auto, all") {
		t.Fatalf("unknown agent error must name the pseudo-ids, got %v", err)
	}
}

// An explicit --agent that names nothing is a mistake — `--agent "$AGENT"`
// with the variable unset — not a way to spell --no-agent. pflag reads the
// flag as CSV, so the empty value parses to an empty slice and would
// otherwise skip the agent side with exit 0.
func TestResolveHookTargetsRejectsEmptyAgent(t *testing.T) {
	for _, args := range [][]string{{"--agent", ""}, {"--agent", "", "--no-vcs"}} {
		_, err := targetsFor(t, args...)
		if err == nil {
			t.Fatalf("%v was accepted, want an error", args)
		}
		if !strings.Contains(err.Error(), "unsupported --agent") {
			t.Fatalf("%v: error = %v, want it to mention \"unsupported --agent\"", args, err)
		}
	}
}

// --scope is checked once here rather than per adapter: only two of the four
// have a per-scope location, so a typo would otherwise pass silently for the
// other two and write their user-level file.
func TestResolveHookTargetsRejectsUnknownScope(t *testing.T) {
	_, err := targetsFor(t, "--scope", "projct")
	if err == nil {
		t.Fatal("--scope projct was accepted, want an error")
	}
	for _, want := range []string{"unsupported --scope", harness.ScopeLocal, harness.ScopeProject} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want it to mention %q", err, want)
		}
	}
	for _, scope := range []string{harness.ScopeLocal, harness.ScopeProject} {
		if _, err := targetsFor(t, "--scope", scope); err != nil {
			t.Fatalf("--scope %s: %v", scope, err)
		}
	}
}

func TestResolveHookTargetsOptOuts(t *testing.T) {
	got, err := targetsFor(t, "--no-vcs")
	if err != nil {
		t.Fatalf("--no-vcs: %v", err)
	}
	if got.vcs != "" || len(got.agents) != 1 || got.agents[0] != "auto" {
		t.Fatalf("--no-vcs targets = %+v, want vcs empty, agents [auto]", got)
	}

	got, err = targetsFor(t, "--no-agent")
	if err != nil {
		t.Fatalf("--no-agent: %v", err)
	}
	if got.vcs != vcsGit || len(got.agents) != 0 {
		t.Fatalf("--no-agent targets = %+v, want vcs %q, agents empty", got, vcsGit)
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

// --skills is independent of vcs/agents (it writes outside the hook config),
// so --no-vcs --no-agent --skills — the natural way to publish skills
// without touching hook config at all — must not trip the nothing-to-do
// guard.
func TestResolveHookTargetsSkillsAloneIsNotNothingToDo(t *testing.T) {
	got, err := targetsFor(t, "--no-vcs", "--no-agent", "--skills")
	if err != nil {
		t.Fatalf("--no-vcs --no-agent --skills: want acceptance, got %v", err)
	}
	if got.vcs != "" || len(got.agents) != 0 || !got.skills {
		t.Fatalf("targets = %+v, want vcs empty, agents empty, skills true", got)
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
	got, err := targetsFor(t, "--no-vcs", "--agent", claudeCode)
	if err != nil {
		t.Fatalf("--no-vcs --agent claude-code: %v", err)
	}
	if got.vcs != "" || len(got.agents) != 1 || got.agents[0] != claudeCode {
		t.Fatalf("targets = %+v, want vcs empty, agents [%s]", got, claudeCode)
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
	if scope != harness.ScopeLocal {
		t.Fatalf("scope default = %q, want %q", scope, harness.ScopeLocal)
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

func TestInstallHooksEnablesWorktreeConfigExtension(t *testing.T) {
	root := initGitRepo(t)
	if _, err := installHooks(discardCmd(), root, hookTargets{vcs: vcsGit}, harness.ScopeLocal); err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	out, err := exec.Command("git", "-C", root, "config", "--local", "--get", "extensions.worktreeConfig").Output()
	if err != nil {
		t.Fatalf("git config --get extensions.worktreeConfig: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "true" {
		t.Fatalf("extensions.worktreeConfig = %q, want true", got)
	}

	// Re-running must stay idempotent — no error, still true.
	if _, err := installHooks(discardCmd(), root, hookTargets{vcs: vcsGit}, harness.ScopeLocal); err != nil {
		t.Fatalf("installHooks (second run): %v", err)
	}
}

// TestInstallHooksWarnsButContinuesWhenExtensionRefused pins the enhancement's
// blast radius: a repo where extensions.worktreeConfig cannot be enabled (here
// one with core.worktree set, which EnableWorktreeConfigExtension refuses) must
// still get its pre-commit and agent hooks, with the failure surfacing only as
// a warning.
func TestInstallHooksWarnsButContinuesWhenExtensionRefused(t *testing.T) {
	root := initGitRepo(t)
	if out, err := exec.Command("git", "-C", root, "config", "core.worktree", root).CombinedOutput(); err != nil {
		t.Fatalf("git config core.worktree: %v\n%s", err, out)
	}

	var stderr bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(io.Discard)
	cmd.SetErr(&stderr)

	res, err := installHooks(cmd, root, claudeTargets(vcsGit, false), harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks: %v (a refused worktree config extension must not abort the run)", err)
	}
	if res.VCS == nil || len(res.Agents) != 1 {
		t.Fatalf("result = %+v, want both integrations installed", res)
	}
	if _, err := os.Stat(filepath.Join(res.VCS.HooksDir, "pre-commit")); err != nil {
		t.Fatalf("stat pre-commit hook: %v", err)
	}
	if _, err := os.Stat(res.Agents[0].Path); err != nil {
		t.Fatalf("stat agent settings %s: %v", res.Agents[0].Path, err)
	}
	if !strings.Contains(stderr.String(), "warning: enable git worktree config extension") {
		t.Fatalf("stderr = %q, want a warning about the worktree config extension", stderr.String())
	}
	if out, err := exec.Command("git", "-C", root, "config", "--local", "--get", "extensions.worktreeConfig").CombinedOutput(); err == nil {
		t.Fatalf("extensions.worktreeConfig = %q, want unset after a refusal", strings.TrimSpace(string(out)))
	}
}

// The extension is enabled for whoever needs it — the VCS side's task-id
// stamping, or the status line's read of that stamp — and for nobody else. At
// the CLI this state needs --no-vcs --no-statusline, since the status line is
// on by default and pulls the extension in with it.
func TestInstallHooksSkipsWorktreeConfigExtensionWhenNothingNeedsIt(t *testing.T) {
	root := initGitRepo(t)
	if _, err := installHooks(discardCmd(), root, claudeTargets("", false), harness.ScopeLocal); err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "config", "--local", "--get", "extensions.worktreeConfig").CombinedOutput(); err == nil {
		t.Fatalf("extensions.worktreeConfig = %q, want unset when neither the VCS side nor the status line is targeted",
			strings.TrimSpace(string(out)))
	}
}

func TestInstallHooksBothIntegrations(t *testing.T) {
	root := initGitRepo(t)

	res, err := installHooks(discardCmd(), root, claudeTargets(vcsGit, false), harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	if res.VCS == nil {
		t.Fatal("VCS result missing")
	}
	if !fileExists(filepath.Join(res.VCS.HooksDir, "pre-commit")) {
		t.Fatalf("pre-commit not written into %s", res.VCS.HooksDir)
	}
	if len(res.Agents) != 1 {
		t.Fatalf("agent results = %+v, want exactly one", res.Agents)
	}
	if got := res.Agents[0].Agent; got != claudeCode {
		t.Fatalf("agent = %q, want %q", got, claudeCode)
	}
	if want := filepath.Join(root, ".claude", "settings.local.json"); res.Agents[0].Path != want {
		t.Fatalf("agent path = %q, want %q", res.Agents[0].Path, want)
	}
	if got := res.Agents[0].UnboundEvents; len(got) != 0 {
		t.Fatalf("unbound events = %v, want none for claude-code", got)
	}
	settings := readSettings(t, res.Agents[0].Path)
	if got := harness.HookCommands(settings, "SessionStart"); len(got) != 1 || got[0] != "lode hook session-start" {
		t.Fatalf("SessionStart commands: %v", got)
	}
}

func TestInstallHooksSkipsOptedOutIntegrations(t *testing.T) {
	root := initGitRepo(t)

	res, err := installHooks(discardCmd(), root, hookTargets{vcs: vcsGit}, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks --no-agent: %v", err)
	}
	if len(res.Agents) != 0 {
		t.Fatalf("agent results = %+v, want none when the agent is skipped", res.Agents)
	}
	if fileExists(filepath.Join(root, ".claude", "settings.local.json")) {
		t.Fatal("--no-agent wrote a settings file")
	}

	root2 := initGitRepo(t)
	res2, err := installHooks(discardCmd(), root2, claudeTargets("", false), harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks --no-vcs: %v", err)
	}
	if res2.VCS != nil {
		t.Fatalf("VCS result = %+v, want nil when the VCS is skipped", res2.VCS)
	}
	hooksDir, err := githooks.Dir(root2)
	if err != nil {
		t.Fatalf("githooks.Dir: %v", err)
	}
	if fileExists(filepath.Join(hooksDir, "pre-commit")) {
		t.Fatal("--no-vcs wrote a pre-commit hook")
	}
}

func TestUninstallHooksUndoesInstall(t *testing.T) {
	root := initGitRepo(t)
	targets := claudeTargets(vcsGit, false)
	if _, err := installHooks(discardCmd(), root, targets, harness.ScopeLocal); err != nil {
		t.Fatalf("installHooks: %v", err)
	}

	res, err := uninstallHooks(root, targets, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("uninstallHooks: %v", err)
	}
	if res.VCS == nil || actionFor(t, res.VCS.Hooks, "pre-commit") != githooks.ActionRemoved {
		t.Fatalf("VCS result = %+v, want action %q", res.VCS, githooks.ActionRemoved)
	}
	if fileExists(filepath.Join(res.VCS.HooksDir, "pre-commit")) {
		t.Fatal("pre-commit still present after uninstall")
	}
	if len(res.Agents) != 1 {
		t.Fatalf("agent results = %+v, want exactly one", res.Agents)
	}
	settings := readSettings(t, res.Agents[0].Path)
	if got := harness.HookCommands(settings, "SessionStart"); len(got) != 0 {
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
	res, err := installHooks(discardCmd(), root, hookTargets{vcs: vcsGit}, harness.ScopeLocal)
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
	if _, ok := decoded["agents"]; ok {
		t.Fatalf("JSON has an agents key for a skipped integration: %s", b)
	}
}

// --- ISSUE 1: partial failure still reports what already landed -----------

// writeCorruptSettings seeds root's local Claude settings file with invalid
// JSON, so the claude-code adapter's hook install/uninstall fails on the agent
// step while leaving any prior VCS step's result standing.
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

	res, err := installHooks(discardCmd(), root, claudeTargets(vcsGit, false), harness.ScopeLocal)
	if err == nil {
		t.Fatal("installHooks with corrupt settings: err = nil, want a parse error")
	}
	if res.VCS == nil {
		t.Fatal("partial result missing VCS: the git hook step already succeeded")
	}
	if !fileExists(filepath.Join(res.VCS.HooksDir, "pre-commit")) {
		t.Fatal("pre-commit not written despite the VCS step succeeding")
	}
	if len(res.Agents) != 0 {
		t.Fatalf("agent results = %+v, want none: the agent step failed", res.Agents)
	}
}

func TestUninstallHooksReturnsPartialResultOnAgentFailure(t *testing.T) {
	root := initGitRepo(t)
	targets := claudeTargets(vcsGit, false)
	if _, err := installHooks(discardCmd(), root, targets, harness.ScopeLocal); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	writeCorruptSettings(t, root)

	res, err := uninstallHooks(root, targets, harness.ScopeLocal)
	if err == nil {
		t.Fatal("uninstallHooks with corrupt settings: err = nil, want a parse error")
	}
	if res.VCS == nil || actionFor(t, res.VCS.Hooks, "pre-commit") != githooks.ActionRemoved {
		t.Fatalf("partial result VCS = %+v, want action %q: the git hook step already succeeded", res.VCS, githooks.ActionRemoved)
	}
	if fileExists(filepath.Join(res.VCS.HooksDir, "pre-commit")) {
		t.Fatal("pre-commit still present: the VCS uninstall step should have removed it")
	}
	if len(res.Agents) != 0 {
		t.Fatalf("agent results = %+v, want none: the agent step failed", res.Agents)
	}
}

func TestInstallCmdReportsPartialResultBeforeFailing(t *testing.T) {
	root := initGitRepo(t)
	isolateHarnessConfig(t)
	writeCorruptSettings(t, root)
	t.Chdir(root)

	out, err := runLode(t, "install")
	if err == nil {
		t.Fatal("install with corrupt settings: err = nil, want a parse error")
	}
	hooksDir, herr := githooks.Dir(root)
	if herr != nil {
		t.Fatalf("githooks.Dir: %v", herr)
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
	isolateHarnessConfig(t)
	if _, err := installHooks(discardCmd(), root, claudeTargets(vcsGit, false), harness.ScopeLocal); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	writeCorruptSettings(t, root)
	t.Chdir(root)

	out, err := runLode(t, "uninstall")
	if err == nil {
		t.Fatal("uninstall with corrupt settings: err = nil, want a parse error")
	}
	hooksDir, herr := githooks.Dir(root)
	if herr != nil {
		t.Fatalf("githooks.Dir: %v", herr)
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
	res := uninstallResult{VCS: &vcsUninstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks",
		Hooks: []githooks.Removal{{Hook: "pre-commit", Action: "bogus"}}}}
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

	out, err := runLode(t, "uninstall", "--agent", claudeCode)
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
		VCS: &vcsInstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks",
			Hooks: []githooks.Chain{{Hook: "pre-commit", ChainedTo: "/repo/.git/hooks/pre-commit.pre-lode"}}},
		Agents: []agentInstall{{Agent: claudeCode, Path: "/repo/.claude/settings.local.json",
			Bound: []string{"SessionStart"}}},
	}
	if err := reportInstall(cmd, res); err != nil {
		t.Fatalf("reportInstall: %v", err)
	}
	want := "git: installed pre-commit hook in /repo/.git/hooks\n" +
		"git: pre-commit chains to /repo/.git/hooks/pre-commit.pre-lode\n" +
		"claude-code: installed hooks in /repo/.claude/settings.local.json\n"
	if buf.String() != want {
		t.Fatalf("output = %q, want %q", buf.String(), want)
	}
}

func TestReportInstallNoChainLineWhenNothingToChainTo(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	res := installResult{VCS: &vcsInstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks",
		Hooks: []githooks.Chain{{Hook: "pre-commit"}}}}
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
		{"removed", githooks.ActionRemoved, "git: removed pre-commit hook from /repo/.git/hooks\n"},
		{"restored", githooks.ActionRestored, "git: removed pre-commit hook from /repo/.git/hooks and restored the previous one\n"},
		{"none", githooks.ActionNone, "git: no Worklode pre-commit hook in /repo/.git/hooks\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			res := uninstallResult{VCS: &vcsUninstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks",
				Hooks: []githooks.Removal{{Hook: "pre-commit", Action: tc.action}}}}
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
		{"removed", harness.ActionRemoved, "claude-code: removed hooks from /repo/.claude/settings.local.json\n"},
		{"none", harness.ActionNone, "claude-code: no Worklode hooks in /repo/.claude/settings.local.json\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			res := uninstallResult{Agents: []agentUninstall{{Agent: claudeCode, Path: "/repo/.claude/settings.local.json", Action: tc.action}}}
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
	res := installResult{VCS: &vcsInstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks",
		Hooks: []githooks.Chain{{Hook: "pre-commit"}}}}
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
	if _, ok := decoded["agents"]; ok {
		t.Fatalf("JSON has an agents key for a skipped integration: %s", buf.String())
	}
}

func TestReportUninstallJSON(t *testing.T) {
	var buf bytes.Buffer
	cmd := jsonCmd(&buf)
	res := uninstallResult{
		VCS: &vcsUninstall{VCS: vcsGit, HooksDir: "/repo/.git/hooks",
			Hooks: []githooks.Removal{{Hook: "pre-commit", Action: githooks.ActionRemoved}}},
		Agents: []agentUninstall{{Agent: claudeCode, Path: "/repo/.claude/settings.local.json", Action: harness.ActionNone}},
	}
	if err := reportUninstall(cmd, res); err != nil {
		t.Fatalf("reportUninstall: %v", err)
	}
	var decoded struct {
		VCS    struct{ Hooks []githooks.Removal } `json:"vcs"`
		Agents []struct{ Action string }          `json:"agents"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s: %v", buf.String(), err)
	}
	if got := actionFor(t, decoded.VCS.Hooks, "pre-commit"); got != githooks.ActionRemoved {
		t.Fatalf("vcs pre-commit action = %q, want %q", got, githooks.ActionRemoved)
	}
	if len(decoded.Agents) != 1 || decoded.Agents[0].Action != harness.ActionNone {
		t.Fatalf("agents = %+v, want one entry with action %q", decoded.Agents, harness.ActionNone)
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
	if _, ok := decoded["agents"]; ok {
		t.Fatalf("JSON has an agents key for a skipped integration: %s", buf.String())
	}
}

// TestUninstallCmdJSONIncludesActionOnBothSides mirrors the install e2e test
// for uninstall: both integrations must carry an "action" key in real --json
// output, not just in the struct.
func TestUninstallCmdJSONIncludesActionOnBothSides(t *testing.T) {
	root := initGitRepo(t)
	isolateHarnessConfig(t)
	if _, err := installHooks(discardCmd(), root, claudeTargets(vcsGit, false), harness.ScopeLocal); err != nil {
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
		VCS    struct{ Hooks []githooks.Removal } `json:"vcs"`
		Agents []struct{ Action string }          `json:"agents"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s: %v", buf.String(), err)
	}
	if got := actionFor(t, decoded.VCS.Hooks, "pre-commit"); got != githooks.ActionRemoved {
		t.Fatalf("vcs pre-commit action = %q, want %q", got, githooks.ActionRemoved)
	}
	if len(decoded.Agents) != 1 || decoded.Agents[0].Action != harness.ActionRemoved {
		t.Fatalf("agents = %+v, want one entry with action %q", decoded.Agents, harness.ActionRemoved)
	}
}

// --- the harness dimension: auto, per-agent stanzas, unbound events --------

// registeredAgent names an adapter a test needs by behaviour, failing loudly
// rather than silently skipping if the registry no longer carries it.
func registeredAgent(t *testing.T, id string) string {
	t.Helper()
	if _, ok := harness.Get(id); !ok {
		t.Fatalf("test needs the %q adapter; registry has %v", id, harness.IDs())
	}
	return id
}

// isolateHarnessConfig points every adapter's config location at scratch
// paths, so an install driven by these tests can never reach the developer's
// own harness config — CODEX_HOME and COPILOT_HOME are honoured by those
// adapters' Detect, so redirecting HOME alone is not enough.
//
// Any test that reaches installHooks or uninstallHooks with the agent left
// unpinned needs this: --agent defaults to auto, and auto is resolveAgents ->
// harness.Detected, which walks every adapter. Without it, `lode uninstall`
// under test deletes the real $COPILOT_HOME/hooks/worklode.json. Passing
// hookTargets with a nil agents list is safe — resolveAgents only expands the
// literal "auto" — as is any explicit --agent or --no-agent.
//
// The paths are absent rather than merely empty: every adapter detects on its
// config location existing, so a test that redirected to a live directory
// would silently detect all four and change what `--agent auto` means. Install
// still works, since writing a hooks file creates its parents.
func isolateHarnessConfig(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), "copilot"))
	t.Setenv("AMP_SETTINGS_FILE", filepath.Join(t.TempDir(), "amp", "settings.json"))
}

// TestInstallReportsPerAgentWithUnboundEvents pins the report's list shape: one
// stanza per agent, self-describing, and — since claude-code binds every event
// — with unbound_events omitted rather than rendered empty. Every harness
// config location is redirected, so detection depends only on the repo.
func TestInstallReportsPerAgentWithUnboundEvents(t *testing.T) {
	root := initGitRepo(t)
	isolateHarnessConfig(t)
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}

	res, err := installHooks(discardCmd(), root, hookTargets{agents: []string{"auto"}}, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks --agent auto: %v", err)
	}
	if len(res.Agents) != 1 || res.Agents[0].Agent != claudeCode {
		t.Fatalf("agents = %+v, want one claude-code stanza", res.Agents)
	}
	if want := filepath.Join(root, ".claude", "settings.local.json"); res.Agents[0].Path != want {
		t.Fatalf("path = %q, want %q", res.Agents[0].Path, want)
	}
	if got := res.Agents[0].UnboundEvents; len(got) != 0 {
		t.Fatalf("unbound_events = %v, want none: claude-code binds every event", got)
	}
	if len(res.Agents[0].Bound) == 0 {
		t.Fatal("bound is empty; the stanza must name what it wrote")
	}

	var buf bytes.Buffer
	if err := reportInstall(jsonCmd(&buf), res); err != nil {
		t.Fatalf("reportInstall: %v", err)
	}
	var decoded struct {
		Agents []struct {
			Agent         string   `json:"agent"`
			Path          string   `json:"path"`
			UnboundEvents []string `json:"unbound_events"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s: %v", buf.String(), err)
	}
	if len(decoded.Agents) != 1 || decoded.Agents[0].Agent != claudeCode {
		t.Fatalf("JSON agents = %+v: %s", decoded.Agents, buf.String())
	}
	if !strings.HasSuffix(decoded.Agents[0].Path, filepath.Join(".claude", "settings.local.json")) {
		t.Fatalf("JSON path = %q", decoded.Agents[0].Path)
	}
	if len(decoded.Agents[0].UnboundEvents) != 0 {
		t.Fatalf("JSON unbound_events = %v, want none", decoded.Agents[0].UnboundEvents)
	}
}

// Detection is repo-scoped, so it must resolve the repo root: a run from a
// subdirectory of a repo whose only harness signal is a root-level .claude/
// must still find claude-code, or `--agent auto` would mean different things
// in different directories of the same repo.
func TestInstallHooksAutoDetectsFromASubdirectory(t *testing.T) {
	root := initGitRepo(t)
	isolateHarnessConfig(t)
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	sub := filepath.Join(root, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sub, err)
	}

	res, err := installHooks(discardCmd(), sub, hookTargets{agents: []string{agentAuto}}, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks from %s: %v", sub, err)
	}
	if len(res.Agents) != 1 || res.Agents[0].Agent != claudeCode {
		t.Fatalf("agents = %+v, want claude-code detected from the repo root", res.Agents)
	}
	// The uninstall side resolves the same way, or it could not remove what
	// the install just wrote.
	ures, err := uninstallHooks(sub, hookTargets{agents: []string{agentAuto}}, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("uninstallHooks from %s: %v", sub, err)
	}
	if len(ures.Agents) != 1 || ures.Agents[0].Agent != claudeCode {
		t.Fatalf("agents = %+v, want claude-code detected from the repo root", ures.Agents)
	}
}

// A repo with no harness configured for it or the user writes nothing and
// succeeds — spec 008 §4 row 1 — but says so rather than going silent.
func TestInstallHooksAutoDetectsNothing(t *testing.T) {
	root := initGitRepo(t)
	isolateHarnessConfig(t)

	var stderr bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(io.Discard)
	cmd.SetErr(&stderr)

	res, err := installHooks(cmd, root, hookTargets{vcs: vcsGit, agents: []string{"auto"}}, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks with nothing to detect: %v", err)
	}
	if len(res.Agents) != 0 {
		t.Fatalf("agents = %+v, want none when nothing is detected", res.Agents)
	}
	if res.VCS == nil {
		t.Fatal("the VCS side must still install when no agent is detected")
	}
	if !strings.Contains(stderr.String(), "no coding agent detected") {
		t.Fatalf("stderr = %q, want it to say no agent was detected", stderr.String())
	}
}

// An explicitly named harness installs even when nothing would detect it:
// asking for it is the detection signal (spec 008 §3.2).
func TestInstallHooksNamedAgentInstallsUndetected(t *testing.T) {
	root := initGitRepo(t)
	isolateHarnessConfig(t)

	res, err := installHooks(discardCmd(), root, claudeTargets("", false), harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks --agent claude-code: %v", err)
	}
	if len(res.Agents) != 1 {
		t.Fatalf("agents = %+v, want the named harness installed anyway", res.Agents)
	}
}

// Agents are installed in the order named, one stanza each — the report reads
// as the run happened rather than in registry order.
func TestInstallHooksWalksAgentsInTheOrderGiven(t *testing.T) {
	root := initGitRepo(t)
	isolateHarnessConfig(t)
	want := []string{registeredAgent(t, "codex"), registeredAgent(t, claudeCode)}

	res, err := installHooks(discardCmd(), root, hookTargets{agents: want}, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	if len(res.Agents) != len(want) {
		t.Fatalf("agents = %+v, want one stanza per named agent %v", res.Agents, want)
	}
	for i, id := range want {
		if res.Agents[i].Agent != id {
			t.Fatalf("agents = %+v, want them in the order given %v", res.Agents, want)
		}
	}
}

// A failure on the second agent keeps the first agent's stanza: uninstall and
// install both report what actually landed rather than discarding it.
func TestInstallHooksReturnsEarlierAgentsWhenALaterOneFails(t *testing.T) {
	root := initGitRepo(t)
	isolateHarnessConfig(t)
	// The codex adapter refuses to rewrite a config it cannot parse, which is
	// the natural way to fail the second agent and only the second.
	codexHome := os.Getenv("CODEX_HOME")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", codexHome, err)
	}
	corrupt := filepath.Join(codexHome, "hooks.json")
	if err := os.WriteFile(corrupt, []byte("not json"), 0o644); err != nil {
		t.Fatalf("seed corrupt codex config: %v", err)
	}
	first, second := registeredAgent(t, claudeCode), registeredAgent(t, "codex")

	res, err := installHooks(discardCmd(), root, hookTargets{agents: []string{first, second}}, harness.ScopeLocal)
	if err == nil {
		t.Fatal("installHooks with a corrupt codex config: err = nil, want a parse error")
	}
	if len(res.Agents) != 1 || res.Agents[0].Agent != first {
		t.Fatalf("agents = %+v, want the first agent's stanza kept", res.Agents)
	}
	if got, err := os.ReadFile(corrupt); err != nil || string(got) != "not json" {
		t.Fatalf("the unparseable config was rewritten: %s %v", got, err)
	}
}

// An adapter with no status-line slot contributes no stanza at all — not one
// carrying an empty action.
func TestInstallHooksSkipsStatusLineForAdapterWithoutOne(t *testing.T) {
	root := initGitRepo(t)
	isolateHarnessConfig(t)
	id := registeredAgent(t, "amp")
	h, _ := harness.Get(id)
	if _, ok := h.(harness.StatusLiner); ok {
		t.Fatalf("%s gained a status line; this test needs an adapter without one", id)
	}

	res, err := installHooks(discardCmd(), root, hookTargets{agents: []string{id}, statusLine: true}, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	if len(res.Agents) != 1 {
		t.Fatalf("agents = %+v, want one stanza", res.Agents)
	}
	if len(res.StatusLine) != 0 {
		t.Fatalf("status_line = %+v, want no stanza for an adapter without the slot", res.StatusLine)
	}
}

// TestReportInstallNamesUnboundEventsAndNotes covers the two report lines no
// registered adapter produces yet: a harness that cannot bind every event, and
// one with advice to pass on.
func TestReportInstallNamesUnboundEventsAndNotes(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(&buf)
	res := installResult{Agents: []agentInstall{{
		Agent:         "someagent",
		Path:          "/home/u/.someagent/config.toml",
		Bound:         []string{"SessionStart"},
		UnboundEvents: []string{"worktree-enter"},
		Notes:         []string{"run /hooks to approve them"},
	}}}
	if err := reportInstall(cmd, res); err != nil {
		t.Fatalf("reportInstall: %v", err)
	}
	want := "someagent: installed hooks in /home/u/.someagent/config.toml " +
		"(no binding for: worktree-enter; git pre-commit still covers the heartbeat)\n" +
		"someagent: run /hooks to approve them\n"
	if buf.String() != want {
		t.Fatalf("output = %q, want %q", buf.String(), want)
	}
}

// An adapter that binds nothing — amp — must not be reported as having
// installed hooks into a file it never opened, and need not exist.
func TestReportInstallSaysNothingWasBoundWhenNothingWas(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(&buf)
	res := installResult{Agents: []agentInstall{{
		Agent:         registeredAgent(t, "amp"),
		Path:          "/home/u/.config/amp/settings.json",
		UnboundEvents: []string{"session-start"},
		Notes:         []string{"amp binds nothing"},
	}}}
	if err := reportInstall(cmd, res); err != nil {
		t.Fatalf("reportInstall: %v", err)
	}
	if strings.Contains(buf.String(), "installed hooks in") {
		t.Fatalf("output claims a write that never happened:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "amp: bound no hooks") {
		t.Fatalf("output = %q, want it to say nothing was bound", buf.String())
	}
}

// TestInstallSkillsPublishesAllDoorways drives installHooks with --skills'
// equivalent (targets.skills) directly, so it needs no server and no
// Postgres. It pins acceptance 9 (spec 008): one store copy reaches Codex,
// Copilot, Amp and Claude Code's skill directories, none of them ever list
// the store's hash directory as if it were a skill.
func TestInstallSkillsPublishesAllDoorways(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	skillsRoot := t.TempDir()
	t.Setenv("LODE_SKILLS_DIR", skillsRoot)

	dirs, err := skillstore.DefaultDirs()
	if err != nil {
		t.Fatalf("DefaultDirs: %v", err)
	}
	content := "# tdd\n\nDo the thing.\n"
	archive := buildTarGz(t, map[string]string{"SKILL.md": content})
	hash := skillhash.Sum([]skillhash.File{{Path: "SKILL.md", Data: []byte(content)}})
	versionDir, err := skillstore.Ensure(dirs, "tdd", hash, func() ([]byte, error) { return archive, nil })
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	wantVersion, err := filepath.EvalSymlinks(versionDir)
	if err != nil {
		t.Fatalf("resolve version dir: %v", err)
	}

	root := initGitRepo(t)
	targets := hookTargets{skills: true}
	res, err := installHooks(discardCmd(), root, targets, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks --skills: %v", err)
	}
	if len(res.Skills) == 0 {
		t.Fatal("Skills report is empty, want one entry per deduped target")
	}

	agentsSkills := filepath.Join(homeDir, ".agents", "skills")
	info, err := os.Lstat(agentsSkills)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink: info=%v err=%v", agentsSkills, info, err)
	}
	if got, err := os.Readlink(agentsSkills); err != nil || got != dirs.Links {
		t.Fatalf("readlink %s = %q, %v, want %q", agentsSkills, got, err, dirs.Links)
	}
	if entries, err := os.ReadDir(agentsSkills); err != nil {
		t.Fatalf("read %s: %v", agentsSkills, err)
	} else {
		for _, e := range entries {
			if e.Name() == "store" || e.Name() == ".store" {
				t.Fatalf("%s lists a store hash directory as a skill: %s", agentsSkills, e.Name())
			}
		}
	}

	claudeSkill := filepath.Join(homeDir, ".claude", "skills", "tdd")
	resolved, err := filepath.EvalSymlinks(claudeSkill)
	if err != nil {
		t.Fatalf("resolve %s: %v", claudeSkill, err)
	}
	if resolved != wantVersion {
		t.Fatalf("%s resolves to %q, want %q", claudeSkill, resolved, wantVersion)
	}
	claudeSkillsDir := filepath.Join(homeDir, ".claude", "skills")
	if entries, err := os.ReadDir(claudeSkillsDir); err != nil {
		t.Fatalf("read %s: %v", claudeSkillsDir, err)
	} else {
		for _, e := range entries {
			if e.Name() == "store" || e.Name() == ".store" {
				t.Fatalf("%s lists a store hash directory as a skill: %s", claudeSkillsDir, e.Name())
			}
		}
	}

	// The Claude Code target is PerSkill: its first-run action must read
	// "per-skill", never "linked" — "linked" would misreport the whole
	// user-owned ~/.claude/skills dir as replaced by a symlink into the
	// store, exactly what spec 008 §17.3 forbids doing.
	var sawClaudeSkills bool
	for _, s := range res.Skills {
		if s.Path != claudeSkillsDir {
			continue
		}
		sawClaudeSkills = true
		if s.Action != "per-skill" {
			t.Fatalf("first run action for %s = %q, want per-skill", s.Path, s.Action)
		}
	}
	if !sawClaudeSkills {
		t.Fatalf("no Skills entry for %s in %+v", claudeSkillsDir, res.Skills)
	}

	// Re-running must be idempotent: unchanged/per-skill outcomes, never an
	// error, and no drift in what got published.
	res2, err := installHooks(discardCmd(), root, targets, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks --skills (second run): %v", err)
	}
	if len(res2.Skills) != len(res.Skills) {
		t.Fatalf("second run skills = %+v, want %d entries", res2.Skills, len(res.Skills))
	}
	for _, s := range res2.Skills {
		switch s.Action {
		case "unchanged", "per-skill":
		default:
			t.Fatalf("second run action = %q for %s, want a stable no-op result", s.Action, s.Path)
		}
	}
}

// TestInstallSkillsStandaloneWithoutVCSOrAgent covers `lode install --no-vcs
// --no-agent --skills`: the natural way to publish skills without touching
// hook config at all. Regression test for the nothing-to-do guard once
// having rejected --skills-only runs outright.
func TestInstallSkillsStandaloneWithoutVCSOrAgent(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	skillsRoot := t.TempDir()
	t.Setenv("LODE_SKILLS_DIR", skillsRoot)

	dirs, err := skillstore.DefaultDirs()
	if err != nil {
		t.Fatalf("DefaultDirs: %v", err)
	}
	content := "# tdd\n\nDo the thing.\n"
	archive := buildTarGz(t, map[string]string{"SKILL.md": content})
	hash := skillhash.Sum([]skillhash.File{{Path: "SKILL.md", Data: []byte(content)}})
	if _, err := skillstore.Ensure(dirs, "tdd", hash, func() ([]byte, error) { return archive, nil }); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	root := initGitRepo(t)
	targets, err := targetsFor(t, "--no-vcs", "--no-agent", "--skills")
	if err != nil {
		t.Fatalf("targetsFor --no-vcs --no-agent --skills: %v", err)
	}
	res, err := installHooks(discardCmd(), root, targets, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks --no-vcs --no-agent --skills: %v", err)
	}
	if res.VCS != nil {
		t.Fatalf("VCS result = %+v, want nil (--no-vcs)", res.VCS)
	}
	if len(res.Agents) != 0 {
		t.Fatalf("agent results = %+v, want none (--no-agent)", res.Agents)
	}
	if len(res.Skills) == 0 {
		t.Fatal("Skills report is empty, want --skills to still publish with no vcs/agent side")
	}
	claudeSkill := filepath.Join(homeDir, ".claude", "skills", "tdd")
	if _, err := os.Lstat(claudeSkill); err != nil {
		t.Fatalf("stat %s: %v, want it published despite --no-vcs --no-agent", claudeSkill, err)
	}
}
