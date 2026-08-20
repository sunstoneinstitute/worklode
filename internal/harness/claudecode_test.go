package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// readSettings reads a settings file as generic JSON, failing the test if it
// is missing or malformed.
func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

// commandsFor returns every hook command registered for a Claude Code event.
func commandsFor(t *testing.T, settings map[string]any, event string) []string {
	t.Helper()
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	groups, ok := hooks[event].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			continue
		}
		entries, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for _, e := range entries {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if cmd, ok := entry["command"].(string); ok {
				out = append(out, cmd)
			}
		}
	}
	return out
}

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

func TestClaudeInstallScopes(t *testing.T) {
	root := t.TempDir()

	local, err := ClaudeSettingsPath(root, ScopeLocal)
	if err != nil {
		t.Fatalf("local path: %v", err)
	}
	if want := filepath.Join(root, ".claude", "settings.local.json"); local != want {
		t.Fatalf("local scope path: got %s, want %s", local, want)
	}
	project, err := ClaudeSettingsPath(root, ScopeProject)
	if err != nil {
		t.Fatalf("project path: %v", err)
	}
	if want := filepath.Join(root, ".claude", "settings.json"); project != want {
		t.Fatalf("project scope path: got %s, want %s", project, want)
	}
	if _, err := ClaudeSettingsPath(root, "global"); err == nil {
		t.Fatal("unknown scope was accepted")
	}
}

func TestClaudeInstallWritesBindings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "settings.local.json")

	if err := installClaudeHooks(path); err != nil {
		t.Fatalf("install: %v", err)
	}

	settings := readSettings(t, path)
	if got := commandsFor(t, settings, "SessionStart"); len(got) != 1 || got[0] != "lode hook session-start" {
		t.Fatalf("SessionStart commands: %v", got)
	}
	if got := commandsFor(t, settings, "Stop"); len(got) != 1 || got[0] != "lode hook heartbeat" {
		t.Fatalf("Stop commands: %v", got)
	}
	// PostToolUse is matched on a tool name, so it costs nothing per
	// ordinary tool call.
	got := commandsFor(t, settings, "PostToolUse")
	if len(got) != 1 || got[0] != "lode hook worktree-enter" {
		t.Fatalf("PostToolUse commands: %v", got)
	}
	hooks := settings["hooks"].(map[string]any)
	groups := hooks["PostToolUse"].([]any)
	if len(groups) != 1 {
		t.Fatalf("PostToolUse groups: %v, want 1", groups)
	}
	if m := groups[0].(map[string]any)["matcher"]; m != "EnterWorktree" {
		t.Fatalf("PostToolUse matcher: %v, want EnterWorktree", m)
	}
}

// WorktreeCreate and WorktreeRemove are delegation hooks, not notifications:
// registering one makes it *the* worktree creator and disables Claude Code's
// built-in `git worktree add`, so EnterWorktree fails outright unless the hook
// prints the path it created. Worklode only observes, so binding them broke
// EnterWorktree in every repo that ran `lode install`.
func TestClaudeInstallDoesNotBindDelegationHooks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "settings.local.json")

	if err := installClaudeHooks(path); err != nil {
		t.Fatalf("install: %v", err)
	}

	settings := readSettings(t, path)
	for _, event := range []string{"WorktreeCreate", "WorktreeRemove"} {
		if got := commandsFor(t, settings, event); len(got) != 0 {
			t.Errorf("%s must stay unbound (Worklode would become the worktree creator), got %v", event, got)
		}
	}
}

func TestClaudeInstallIsIdempotentAndPreservesForeignSettings(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "settings.local.json")
	existing := `{
	  "permissions": {"allow": ["Bash(go test:*)"]},
	  "hooks": {
	    "Stop": [{"hooks": [{"type": "command", "command": "my-own-tool --report"}]}]
	  }
	}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	if err := installClaudeHooks(path); err != nil {
		t.Fatalf("first install: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after first install: %v", err)
	}
	if err := installClaudeHooks(path); err != nil {
		t.Fatalf("second install: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after second install: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("install is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	settings := readSettings(t, path)
	if _, ok := settings["permissions"]; !ok {
		t.Fatal("install dropped the unrelated permissions block")
	}
	stop := commandsFor(t, settings, "Stop")
	if len(stop) != 2 {
		t.Fatalf("Stop commands: %v, want the foreign hook plus ours", stop)
	}

	action, err := uninstallClaudeHooks(path)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if action != ActionRemoved {
		t.Fatalf("uninstall action = %q, want %q", action, ActionRemoved)
	}
	settings = readSettings(t, path)
	if _, ok := settings["permissions"]; !ok {
		t.Fatal("uninstall dropped the unrelated permissions block")
	}
	if got := commandsFor(t, settings, "Stop"); len(got) != 1 || got[0] != "my-own-tool --report" {
		t.Fatalf("Stop after uninstall: %v, want only the foreign hook", got)
	}
	if got := commandsFor(t, settings, "SessionStart"); len(got) != 0 {
		t.Fatalf("SessionStart after uninstall: %v, want none", got)
	}
}

func TestPropagateClaudeHooksToWorktreeMirrorsRootsOptIn(t *testing.T) {
	root := t.TempDir()
	rootPath := filepath.Join(root, ".claude", "settings.local.json")
	if err := installClaudeHooks(rootPath); err != nil {
		t.Fatalf("install at root: %v", err)
	}
	if _, err := installStatusLine(rootPath); err != nil {
		t.Fatalf("install status line at root: %v", err)
	}

	dir := t.TempDir()
	if err := (ClaudeCode{}).PropagateToWorktree(root, dir); err != nil {
		t.Fatalf("propagate: %v", err)
	}

	dirPath := filepath.Join(dir, ".claude", "settings.local.json")
	settings := readSettings(t, dirPath)
	if got := commandsFor(t, settings, "SessionStart"); len(got) != 1 || got[0] != "lode hook session-start" {
		t.Fatalf("SessionStart in worktree = %v, want the mirrored binding", got)
	}
	sl, ok := settings["statusLine"]
	if !ok || !isLodeStatusLine(sl) {
		t.Fatalf("statusLine in worktree = %v, want the mirrored lode statusline", sl)
	}
}

func TestPropagateClaudeHooksToWorktreeSkipsWhenRootNeverOptedIn(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()

	if err := (ClaudeCode{}).PropagateToWorktree(root, dir); err != nil {
		t.Fatalf("propagate: %v", err)
	}

	dirPath := filepath.Join(dir, ".claude", "settings.local.json")
	if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
		t.Fatalf("expected no settings file to be written, stat err = %v", err)
	}
}

func TestPropagateClaudeHooksToWorktreeLeavesForeignStatusLineAlone(t *testing.T) {
	root := t.TempDir()
	rootPath := filepath.Join(root, ".claude", "settings.local.json")
	if err := installClaudeHooks(rootPath); err != nil {
		t.Fatalf("install at root: %v", err)
	}
	rootSettings := readSettings(t, rootPath)
	rootSettings["statusLine"] = map[string]any{"type": "command", "command": "my-own-statusline"}
	if err := WriteJSONFile(rootPath, rootSettings); err != nil {
		t.Fatalf("seed foreign status line: %v", err)
	}

	dir := t.TempDir()
	if err := (ClaudeCode{}).PropagateToWorktree(root, dir); err != nil {
		t.Fatalf("propagate: %v", err)
	}

	dirPath := filepath.Join(dir, ".claude", "settings.local.json")
	settings := readSettings(t, dirPath)
	if _, ok := settings["statusLine"]; ok {
		t.Fatalf("statusLine in worktree = %v, want none (root's is not ours)", settings["statusLine"])
	}
}

func TestClaudeUninstallWithNoSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	action, err := uninstallClaudeHooks(path)
	if err != nil {
		t.Fatalf("uninstall with no settings file: %v, want nil", err)
	}
	if action != ActionNone {
		t.Fatalf("action = %q, want %q", action, ActionNone)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("uninstall created a settings file")
	}
}

// TestClaudeUninstallNoopLeavesFileUntouched covers the case where the
// settings file exists but has no `lode hook` entries to strip: the action
// must be ActionNone (not the false "removed"), and the file must not be
// rewritten at all — a no-op uninstall reformatting someone's hand-edited
// JSON or bumping its mtime would be its own kind of lie.
func TestClaudeUninstallNoopLeavesFileUntouched(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "settings.local.json")
	content := []byte(`{
  "permissions": {"allow": ["Bash(go test:*)"]},
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "my-own-tool --report"}]}]
  }
}
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	past := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	action, err := uninstallClaudeHooks(path)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if action != ActionNone {
		t.Fatalf("action = %q, want %q (no lode hooks were present)", action, ActionNone)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after uninstall: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("no-op uninstall rewrote file content:\nbefore: %s\nafter:  %s", content, got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after uninstall: %v", err)
	}
	if !info.ModTime().Equal(past) {
		t.Fatalf("no-op uninstall updated mtime: got %v, want unchanged %v", info.ModTime(), past)
	}
}

func TestInstallStatusLineWritesTheCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.local.json")

	action, err := installStatusLine(path)
	if err != nil {
		t.Fatalf("installStatusLine: %v", err)
	}
	if action != ActionInstalled {
		t.Fatalf("action = %q, want %q", action, ActionInstalled)
	}
	if got := statusLineCommand(t, path); got != StatusLineCommand {
		t.Fatalf("statusLine command = %q, want %q", got, StatusLineCommand)
	}
}

// The slot holds exactly one command, so an install that finds someone else's
// status line must decline rather than replace it.
func TestInstallStatusLineKeepsAnExistingOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	if err := WriteJSONFile(path, map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "~/bin/my-statusline"},
	}); err != nil {
		t.Fatal(err)
	}

	action, err := installStatusLine(path)
	if err != nil {
		t.Fatalf("installStatusLine: %v", err)
	}
	if action != ActionKept {
		t.Fatalf("action = %q, want %q", action, ActionKept)
	}
	if got := statusLineCommand(t, path); got != "~/bin/my-statusline" {
		t.Fatalf("statusLine command = %q, want it untouched", got)
	}
}

func TestInstallStatusLineIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	if _, err := installStatusLine(path); err != nil {
		t.Fatal(err)
	}
	action, err := installStatusLine(path)
	if err != nil {
		t.Fatalf("second installStatusLine: %v", err)
	}
	if action != ActionInstalled {
		t.Fatalf("action = %q, want a converging re-install", action)
	}
	if got := statusLineCommand(t, path); got != StatusLineCommand {
		t.Fatalf("statusLine command = %q", got)
	}
}

func TestInstallStatusLinePreservesOtherSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	if err := WriteJSONFile(path, map[string]any{"model": "opus"}); err != nil {
		t.Fatal(err)
	}
	if _, err := installStatusLine(path); err != nil {
		t.Fatal(err)
	}
	if got := readSettings(t, path)["model"]; got != "opus" {
		t.Fatalf("model = %v, want it preserved", got)
	}
}

func TestUninstallStatusLineRemovesOnlyOurs(t *testing.T) {
	tests := []struct {
		name        string
		settings    map[string]any
		wantAction  string
		wantCommand string
	}{
		{
			name:       "ours",
			settings:   map[string]any{"statusLine": map[string]any{"type": "command", "command": StatusLineCommand}},
			wantAction: ActionRemoved,
		},
		{
			name:        "someone else's",
			settings:    map[string]any{"statusLine": map[string]any{"type": "command", "command": "starship prompt"}},
			wantAction:  ActionKept,
			wantCommand: "starship prompt",
		},
		{
			name:       "no status line at all",
			settings:   map[string]any{"model": "opus"},
			wantAction: ActionNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
			if err := WriteJSONFile(path, tt.settings); err != nil {
				t.Fatal(err)
			}
			action, err := uninstallStatusLine(path)
			if err != nil {
				t.Fatalf("uninstallStatusLine: %v", err)
			}
			if action != tt.wantAction {
				t.Fatalf("action = %q, want %q", action, tt.wantAction)
			}
			if got := statusLineCommand(t, path); got != tt.wantCommand {
				t.Fatalf("statusLine command = %q, want %q", got, tt.wantCommand)
			}
		})
	}
}

// A no-op uninstall must not rewrite the file — that would reformat someone's
// settings JSON and bump its mtime for nothing.
func TestUninstallStatusLineLeavesAMissingFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	action, err := uninstallStatusLine(path)
	if err != nil {
		t.Fatalf("uninstallStatusLine: %v", err)
	}
	if action != ActionNone {
		t.Fatalf("action = %q, want %q", action, ActionNone)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("uninstall created a settings file")
	}
}

// The binary may be invoked by absolute path or carry flags, so recognizing
// our own entry cannot be string equality.
func TestIsLodeStatusLine(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"lode statusline", true},
		{"/usr/local/bin/lode statusline", true},
		{"  lode   statusline  ", true},
		{"lode hook heartbeat", false},
		{"lode", false},
		{"my-lode statusline", false},
		{"starship prompt", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := isLodeStatusLine(map[string]any{"type": "command", "command": tt.command})
			if got != tt.want {
				t.Fatalf("isLodeStatusLine(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
	if isLodeStatusLine("a bare string") {
		t.Fatal("a non-object statusLine is not ours")
	}
	if isLodeStatusLine(map[string]any{"type": "command"}) {
		t.Fatal("a statusLine with no command is not ours")
	}
}

// BoundNames must use the same native-event spelling Events() reports, so a
// report of what was bound lines up with the event table.
func TestBoundNamesMatchEvents(t *testing.T) {
	bound := map[string]bool{}
	for _, n := range boundNames() {
		bound[n] = true
	}
	if len(bound) != len(claudeBindings) {
		t.Fatalf("boundNames() = %v, want one entry per binding", boundNames())
	}
	for event, natives := range (ClaudeCode{}).Events() {
		for _, n := range natives {
			if !bound[n] {
				t.Errorf("%s maps to %q, which no binding writes", event, n)
			}
		}
	}
}
