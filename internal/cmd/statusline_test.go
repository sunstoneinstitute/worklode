package cmd

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/harness"
)

// statusLineCommand returns the command string in a settings file's statusLine,
// or "" when there is none.
func statusLineCommand(t *testing.T, path string) string {
	t.Helper()
	settings := readSettings(t, path)
	entry, ok := settings["statusLine"].(map[string]any)
	if !ok {
		return ""
	}
	command, _ := entry["command"].(string)
	return command
}

func TestResolveHookTargetsStatusLineDefaultsOn(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"default", nil, true},
		{"explicit", []string{"--statusline"}, true},
		{"opted out", []string{"--no-statusline"}, false},
		{"negated form", []string{"--statusline=false"}, false},
		// The status line lives in the agent's settings file, so skipping the
		// agent skips it — silently, since the user never asked for it.
		{"agent skipped", []string{"--no-agent"}, false},
		// ...but the VCS side is unrelated to it.
		{"vcs skipped", []string{"--no-vcs"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := targetsFor(t, tt.args...)
			if err != nil {
				t.Fatalf("resolveHookTargets %v: %v", tt.args, err)
			}
			if got.statusLine != tt.want {
				t.Fatalf("statusLine = %v, want %v", got.statusLine, tt.want)
			}
		})
	}
}

func TestResolveHookTargetsRejectsStatusLineContradictions(t *testing.T) {
	tests := [][]string{
		{"--statusline", "--no-statusline"},
		// Explicitly asking for both is a contradiction; inheriting the
		// default alongside --no-agent is not, and is covered above.
		{"--statusline", "--no-agent"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := targetsFor(t, args...); err == nil {
				t.Fatalf("targetsFor(%v) = nil error, want a contradiction", args)
			}
		})
	}
}

func TestInstallHooksWritesStatusLineOnlyWhenTargeted(t *testing.T) {
	root := initGitRepo(t)
	settingsPath := filepath.Join(root, ".claude", "settings.local.json")

	res, err := installHooks(discardCmd(), root, claudeTargets("", false), harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	if len(res.StatusLine) != 0 {
		t.Fatalf("status line result = %+v, want none when not targeted", res.StatusLine)
	}
	if got := statusLineCommand(t, settingsPath); got != "" {
		t.Fatalf("statusLine written when not targeted: %q", got)
	}

	res, err = installHooks(discardCmd(), root, claudeTargets("", true), harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks --statusline: %v", err)
	}
	if len(res.StatusLine) != 1 || res.StatusLine[0].Action != harness.ActionInstalled {
		t.Fatalf("status line result = %+v", res.StatusLine)
	}
	if got := res.StatusLine[0].Agent; got != claudeCode {
		t.Fatalf("status line agent = %q, want %q", got, claudeCode)
	}
	if got := statusLineCommand(t, settingsPath); got != harness.StatusLineCommand {
		t.Fatalf("statusLine command = %q", got)
	}
}

// The status line reads worklode.task-id per worktree, which only works once
// the extension is enabled — so --statusline must enable it even with --no-vcs,
// where nothing else would.
func TestInstallHooksStatusLineEnablesTheWorktreeConfigExtension(t *testing.T) {
	root := initGitRepo(t)
	targets := claudeTargets("", true)
	if _, err := installHooks(discardCmd(), root, targets, harness.ScopeLocal); err != nil {
		t.Fatalf("installHooks --no-vcs --statusline: %v", err)
	}
	out, err := exec.Command("git", "-C", root, "config", "--local", "--get", "extensions.worktreeConfig").Output()
	if err != nil {
		t.Fatalf("git config --get extensions.worktreeConfig: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "true" {
		t.Fatalf("extensions.worktreeConfig = %q, want true", got)
	}
}

func TestUninstallHooksUndoesTheStatusLine(t *testing.T) {
	root := initGitRepo(t)
	targets := claudeTargets(vcsGit, true)
	if _, err := installHooks(discardCmd(), root, targets, harness.ScopeLocal); err != nil {
		t.Fatalf("installHooks: %v", err)
	}

	res, err := uninstallHooks(root, targets, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("uninstallHooks: %v", err)
	}
	if len(res.StatusLine) != 1 || res.StatusLine[0].Action != harness.ActionRemoved {
		t.Fatalf("status line result = %+v", res.StatusLine)
	}
	if got := statusLineCommand(t, filepath.Join(root, ".claude", "settings.local.json")); got != "" {
		t.Fatalf("statusLine command = %q, want it gone", got)
	}
}

func TestReportInstallStatusLineLines(t *testing.T) {
	tests := []struct {
		action string
		want   string
	}{
		{harness.ActionInstalled, "status line set to `lode statusline`"},
		{harness.ActionKept, "kept the status line already configured"},
		{"weird", `unexpected status line result "weird"`},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			var buf bytes.Buffer
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().Bool("json", false, "")
			cmd.SetOut(&buf)
			res := installResult{StatusLine: []statusLineInstall{{
				Agent: claudeCode, Path: "/repo/.claude/settings.local.json", Action: tt.action,
			}}}
			if err := reportInstall(cmd, res); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), tt.want) {
				t.Fatalf("got %q, want it to contain %q", buf.String(), tt.want)
			}
		})
	}
}

func TestReportUninstallStatusLineLines(t *testing.T) {
	tests := []struct {
		action string
		want   string
	}{
		{harness.ActionRemoved, "removed the status line"},
		{harness.ActionKept, "left the status line"},
		{harness.ActionNone, "no status line"},
		{"weird", `unexpected status line result "weird"`},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			var buf bytes.Buffer
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().Bool("json", false, "")
			cmd.SetOut(&buf)
			res := uninstallResult{StatusLine: []statusLineUninstall{{
				Agent: claudeCode, Path: "/repo/.claude/settings.local.json", Action: tt.action,
			}}}
			if err := reportUninstall(cmd, res); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), tt.want) {
				t.Fatalf("got %q, want it to contain %q", buf.String(), tt.want)
			}
		})
	}
}

func TestReportInstallJSONOmitsSkippedStatusLine(t *testing.T) {
	var buf bytes.Buffer
	if err := reportInstall(jsonCmd(&buf), installResult{Agents: []agentInstall{{Agent: claudeCode, Path: "p"}}}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["status_line"]; ok {
		t.Fatalf("status_line present when skipped: %s", buf.String())
	}
}

// The command must print whatever it can and exit 0 whatever stdin holds: the
// harness renders its output, so an error here would land in the user's prompt.
func TestStatuslineCmdNeverFails(t *testing.T) {
	for _, in := range []string{"", "not json", `{"model":{"display_name":"Opus 5"}}`} {
		var out bytes.Buffer
		cmd := newStatuslineCmd()
		cmd.SetIn(strings.NewReader(in))
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(nil)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("stdin %q: %v", in, err)
		}
	}
}

func TestStatuslineCmdRendersTheModel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	var out bytes.Buffer
	cmd := newStatuslineCmd()
	cmd.SetIn(strings.NewReader(`{"model":{"display_name":"Opus 5"},"workspace":{"current_dir":"` + dir + `"}}`))
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Opus 5") {
		t.Fatalf("got %q", out.String())
	}
	if strings.Contains(out.String(), "\n") {
		t.Fatalf("a status line must be one line, got %q", out.String())
	}
}

// Guard the naming decision itself: the command is un-namespaced and takes no
// harness flag, because Claude Code and Cursor CLI share one payload contract
// and the harnesses that differ take no command at all.
func TestStatuslineCmdIsHarnessNeutral(t *testing.T) {
	cmd := newStatuslineCmd()
	if cmd.Use != "statusline" {
		t.Fatalf("Use = %q, want a top-level, un-namespaced %q", cmd.Use, "statusline")
	}
	if f := cmd.Flags().Lookup("agent"); f != nil {
		t.Fatal("statusline grew an --agent flag; there is no second dialect to dispatch on")
	}
	if len(cmd.Commands()) != 0 {
		t.Fatal("statusline grew per-harness subcommands")
	}
}

func TestStatuslineCmdIsRegisteredOnRoot(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		if c.Name() == "statusline" {
			return
		}
	}
	t.Fatal("lode statusline is not registered on the root command")
}
