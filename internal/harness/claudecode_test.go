package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

	local, err := claudeSettingsPath(root, ScopeLocal)
	if err != nil {
		t.Fatalf("local path: %v", err)
	}
	if want := filepath.Join(root, ".claude", "settings.local.json"); local != want {
		t.Fatalf("local scope path: got %s, want %s", local, want)
	}
	project, err := claudeSettingsPath(root, ScopeProject)
	if err != nil {
		t.Fatalf("project path: %v", err)
	}
	if want := filepath.Join(root, ".claude", "settings.json"); project != want {
		t.Fatalf("project scope path: got %s, want %s", project, want)
	}
	if _, err := claudeSettingsPath(root, "global"); err == nil {
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
	if got := HookCommands(settings, "SessionStart"); len(got) != 1 || got[0] != "lode-hook session-start" {
		t.Fatalf("SessionStart commands: %v", got)
	}
	if got := HookCommands(settings, "Stop"); len(got) != 1 || got[0] != "lode-hook heartbeat" {
		t.Fatalf("Stop commands: %v", got)
	}
	// PostToolUse is matched on a tool name, so it costs nothing per
	// ordinary tool call.
	got := HookCommands(settings, "PostToolUse")
	if len(got) != 1 || got[0] != "lode-hook worktree-enter" {
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
		if got := HookCommands(settings, event); len(got) != 0 {
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
	stop := HookCommands(settings, "Stop")
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
	if got := HookCommands(settings, "Stop"); len(got) != 1 || got[0] != "my-own-tool --report" {
		t.Fatalf("Stop after uninstall: %v, want only the foreign hook", got)
	}
	if got := HookCommands(settings, "SessionStart"); len(got) != 0 {
		t.Fatalf("SessionStart after uninstall: %v, want none", got)
	}
}

// seedClaudeSettings writes Worklode's hooks, and optionally its status line,
// into the settings file at path — the opted-in state PropagateToWorktree
// mirrors.
func seedClaudeSettings(t *testing.T, path string, statusLine bool) {
	t.Helper()
	settings, err := ReadJSONFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	applyGroupedHooks(settings, claudeBindings)
	if statusLine {
		applyStatusLine(settings)
	}
	if err := writeJSONFile(path, settings); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
}

func TestPropagateClaudeHooksToWorktreeMirrorsRootsOptIn(t *testing.T) {
	root := t.TempDir()
	rootPath := filepath.Join(root, ".claude", "settings.local.json")
	seedClaudeSettings(t, rootPath, true)

	dir := t.TempDir()
	if err := (ClaudeCode{}).PropagateToWorktree(root, dir); err != nil {
		t.Fatalf("propagate: %v", err)
	}

	dirPath := filepath.Join(dir, ".claude", "settings.local.json")
	settings := readSettings(t, dirPath)
	if got := HookCommands(settings, "SessionStart"); len(got) != 1 || got[0] != "lode-hook session-start" {
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
	if err := writeJSONFile(rootPath, rootSettings); err != nil {
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

// countSettingsWrites makes writeJSONFile count its calls for the rest of the
// test. Hooks and the status line live in one file, so "how many times was it
// written" is the contract the combined install/uninstall paths exist to hold.
func countSettingsWrites(t *testing.T) *int {
	t.Helper()
	writes := 0
	real := writeJSONFile
	writeJSONFile = func(path string, settings map[string]any) error {
		writes++
		return real(path, settings)
	}
	t.Cleanup(func() { writeJSONFile = real })
	return &writes
}

func TestClaudeInstallWithStatusLineWritesTheFileOnce(t *testing.T) {
	root := initGitRepo(t)
	writes := countSettingsWrites(t)

	hi, err := (ClaudeCode{}).InstallWithStatusLine(root, ScopeLocal)
	if err != nil {
		t.Fatalf("InstallWithStatusLine: %v", err)
	}
	if *writes != 1 {
		t.Fatalf("settings writes = %d, want 1 (hooks and status line share one file)", *writes)
	}
	if hi.StatusLine == nil || hi.StatusLine.Action != ActionInstalled {
		t.Fatalf("status line = %+v, want %q", hi.StatusLine, ActionInstalled)
	}
	if hi.StatusLine.Path != hi.Path {
		t.Fatalf("status line path = %s, want the settings path %s", hi.StatusLine.Path, hi.Path)
	}
	settings := readSettings(t, hi.Path)
	if got := HookCommands(settings, "SessionStart"); len(got) != 1 || got[0] != "lode-hook session-start" {
		t.Fatalf("SessionStart commands: %v", got)
	}
	if got := statusLineCommand(t, hi.Path); got != StatusLineCommand {
		t.Fatalf("statusLine command = %q, want %q", got, StatusLineCommand)
	}
}

// A status line someone else configured is still declined, and declining it
// must not cost the hooks their write.
func TestClaudeInstallWithStatusLineKeepsAForeignOne(t *testing.T) {
	root := initGitRepo(t)
	path, err := claudeSettingsPath(root, ScopeLocal)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(path, map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "starship prompt"},
	}); err != nil {
		t.Fatal(err)
	}
	writes := countSettingsWrites(t)

	hi, err := (ClaudeCode{}).InstallWithStatusLine(root, ScopeLocal)
	if err != nil {
		t.Fatalf("InstallWithStatusLine: %v", err)
	}
	if *writes != 1 {
		t.Fatalf("settings writes = %d, want 1", *writes)
	}
	if hi.StatusLine == nil || hi.StatusLine.Action != ActionKept {
		t.Fatalf("status line = %+v, want %q", hi.StatusLine, ActionKept)
	}
	if got := statusLineCommand(t, path); got != "starship prompt" {
		t.Fatalf("statusLine command = %q, want it untouched", got)
	}
	if got := HookCommands(readSettings(t, path), "Stop"); len(got) != 1 {
		t.Fatalf("Stop commands = %v, want ours despite the kept status line", got)
	}
}

func TestClaudeUninstallWithStatusLineWritesTheFileOnce(t *testing.T) {
	root := initGitRepo(t)
	if _, err := (ClaudeCode{}).InstallWithStatusLine(root, ScopeLocal); err != nil {
		t.Fatalf("install: %v", err)
	}
	writes := countSettingsWrites(t)

	hu, err := (ClaudeCode{}).UninstallWithStatusLine(root, ScopeLocal)
	if err != nil {
		t.Fatalf("UninstallWithStatusLine: %v", err)
	}
	if *writes != 1 {
		t.Fatalf("settings writes = %d, want 1", *writes)
	}
	if hu.Action != ActionRemoved {
		t.Fatalf("hooks action = %q, want %q", hu.Action, ActionRemoved)
	}
	if hu.StatusLine == nil || hu.StatusLine.Action != ActionRemoved {
		t.Fatalf("status line = %+v, want %q", hu.StatusLine, ActionRemoved)
	}
	settings := readSettings(t, hu.Path)
	if _, ok := settings["hooks"]; ok {
		t.Fatalf("hooks left behind: %v", settings["hooks"])
	}
	if got := statusLineCommand(t, hu.Path); got != "" {
		t.Fatalf("statusLine command = %q, want it gone", got)
	}
}

func TestClaudeUninstallWithStatusLineRemovesLegacyStatusLine(t *testing.T) {
	root := initGitRepo(t)
	path, err := claudeSettingsPath(root, ScopeLocal)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(path, map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "/usr/local/bin/lode statusline"},
	}); err != nil {
		t.Fatal(err)
	}

	hu, err := (ClaudeCode{}).UninstallWithStatusLine(root, ScopeLocal)
	if err != nil {
		t.Fatalf("UninstallWithStatusLine: %v", err)
	}
	if hu.StatusLine == nil || hu.StatusLine.Action != ActionRemoved {
		t.Fatalf("status line = %+v, want %q", hu.StatusLine, ActionRemoved)
	}
	if got := statusLineCommand(t, path); got != "" {
		t.Fatalf("statusLine command = %q, want it gone", got)
	}
}

// Nothing of ours in the file means nothing written at all: a no-op uninstall
// must not reformat someone's settings JSON or bump its mtime, and that has to
// stay true now that one write covers both surfaces.
func TestClaudeUninstallWithStatusLineNoopLeavesFileUntouched(t *testing.T) {
	root := initGitRepo(t)
	path, err := claudeSettingsPath(root, ScopeLocal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "my-own-tool --report"}]}]
  },
  "statusLine": {"type": "command", "command": "starship prompt"}
}
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	writes := countSettingsWrites(t)

	hu, err := (ClaudeCode{}).UninstallWithStatusLine(root, ScopeLocal)
	if err != nil {
		t.Fatalf("UninstallWithStatusLine: %v", err)
	}
	if *writes != 0 {
		t.Fatalf("settings writes = %d, want 0 for a no-op", *writes)
	}
	if hu.Action != ActionNone {
		t.Fatalf("hooks action = %q, want %q", hu.Action, ActionNone)
	}
	if hu.StatusLine == nil || hu.StatusLine.Action != ActionKept {
		t.Fatalf("status line = %+v, want %q", hu.StatusLine, ActionKept)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("no-op uninstall rewrote file content:\nbefore: %s\nafter:  %s", content, got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(past) {
		t.Fatalf("no-op uninstall updated mtime: got %v, want unchanged %v", info.ModTime(), past)
	}
}

// One half of the file being ours is still one write, and the other half must
// survive it.
func TestClaudeUninstallWithStatusLineRemovesOneHalfWithoutTheOther(t *testing.T) {
	root := initGitRepo(t)
	path, err := claudeSettingsPath(root, ScopeLocal)
	if err != nil {
		t.Fatal(err)
	}
	if err := installClaudeHooks(path); err != nil {
		t.Fatal(err)
	}
	settings := readSettings(t, path)
	settings["statusLine"] = map[string]any{"type": "command", "command": "starship prompt"}
	if err := writeJSONFile(path, settings); err != nil {
		t.Fatal(err)
	}
	writes := countSettingsWrites(t)

	hu, err := (ClaudeCode{}).UninstallWithStatusLine(root, ScopeLocal)
	if err != nil {
		t.Fatalf("UninstallWithStatusLine: %v", err)
	}
	if *writes != 1 {
		t.Fatalf("settings writes = %d, want 1", *writes)
	}
	if hu.Action != ActionRemoved {
		t.Fatalf("hooks action = %q, want %q", hu.Action, ActionRemoved)
	}
	if hu.StatusLine == nil || hu.StatusLine.Action != ActionKept {
		t.Fatalf("status line = %+v, want %q", hu.StatusLine, ActionKept)
	}
	if got := statusLineCommand(t, path); got != "starship prompt" {
		t.Fatalf("statusLine command = %q, want the foreign one kept", got)
	}
}

func TestClaudeUninstallWithStatusLineWithNoSettingsFile(t *testing.T) {
	root := initGitRepo(t)
	writes := countSettingsWrites(t)

	hu, err := (ClaudeCode{}).UninstallWithStatusLine(root, ScopeLocal)
	if err != nil {
		t.Fatalf("UninstallWithStatusLine: %v", err)
	}
	if *writes != 0 {
		t.Fatalf("settings writes = %d, want 0", *writes)
	}
	if hu.Action != ActionNone {
		t.Fatalf("hooks action = %q, want %q", hu.Action, ActionNone)
	}
	if hu.StatusLine == nil || hu.StatusLine.Action != ActionNone {
		t.Fatalf("status line = %+v, want %q", hu.StatusLine, ActionNone)
	}
	if _, err := os.Stat(hu.Path); !os.IsNotExist(err) {
		t.Fatal("uninstall created a settings file")
	}
}

// The propagation path writes the worktree's settings file for both surfaces
// too, so it gets the same single-write treatment.
func TestPropagateClaudeHooksToWorktreeWritesTheFileOnce(t *testing.T) {
	root := t.TempDir()
	rootPath := filepath.Join(root, ".claude", "settings.local.json")
	seedClaudeSettings(t, rootPath, true)
	dir := t.TempDir()
	writes := countSettingsWrites(t)

	if err := (ClaudeCode{}).PropagateToWorktree(root, dir); err != nil {
		t.Fatalf("propagate: %v", err)
	}
	if *writes != 1 {
		t.Fatalf("settings writes = %d, want 1", *writes)
	}
	dirPath := filepath.Join(dir, ".claude", "settings.local.json")
	if got := HookCommands(readSettings(t, dirPath), "SessionStart"); len(got) != 1 {
		t.Fatalf("SessionStart in worktree = %v, want the mirrored binding", got)
	}
	if got := statusLineCommand(t, dirPath); got != StatusLineCommand {
		t.Fatalf("statusLine in worktree = %q, want the mirrored one", got)
	}
}

// statusLineIn returns the command string in an in-memory settings object's
// statusLine, or "" when there is none.
func statusLineIn(settings map[string]any) string {
	entry, ok := settings["statusLine"].(map[string]any)
	if !ok {
		return ""
	}
	command, _ := entry["command"].(string)
	return command
}

func TestApplyStatusLineWritesTheCommand(t *testing.T) {
	settings := map[string]any{"model": "opus"}

	if action := applyStatusLine(settings); action != ActionInstalled {
		t.Fatalf("action = %q, want %q", action, ActionInstalled)
	}
	if got := statusLineIn(settings); got != StatusLineCommand {
		t.Fatalf("statusLine command = %q, want %q", got, StatusLineCommand)
	}
	if got := settings["model"]; got != "opus" {
		t.Fatalf("model = %v, want it preserved", got)
	}
}

// The slot holds exactly one command, so an install that finds someone else's
// status line must decline rather than replace it.
func TestApplyStatusLineKeepsAnExistingOne(t *testing.T) {
	settings := map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "~/bin/my-statusline"},
	}

	if action := applyStatusLine(settings); action != ActionKept {
		t.Fatalf("action = %q, want %q", action, ActionKept)
	}
	if got := statusLineIn(settings); got != "~/bin/my-statusline" {
		t.Fatalf("statusLine command = %q, want it untouched", got)
	}
}

func TestApplyStatusLineIsIdempotent(t *testing.T) {
	settings := map[string]any{}
	applyStatusLine(settings)

	if action := applyStatusLine(settings); action != ActionInstalled {
		t.Fatalf("action = %q, want a converging re-install", action)
	}
	if got := statusLineIn(settings); got != StatusLineCommand {
		t.Fatalf("statusLine command = %q", got)
	}
}

func TestStripStatusLineRemovesOnlyOurs(t *testing.T) {
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
			if action := stripStatusLine(tt.settings); action != tt.wantAction {
				t.Fatalf("action = %q, want %q", action, tt.wantAction)
			}
			if got := statusLineIn(tt.settings); got != tt.wantCommand {
				t.Fatalf("statusLine command = %q, want %q", got, tt.wantCommand)
			}
		})
	}
}

// The binary may be invoked by absolute path or carry flags, so recognizing
// our own entry cannot be string equality.
func TestIsLodeStatusLine(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"lode-statusline", true},
		{"lode statusline", true},
		{"/usr/local/bin/lode statusline", true},
		{"  lode   statusline  ", true},
		{"lode hook heartbeat", false},
		{"echo lode statusline", false},
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

func TestClaudeReinstallUpgradesLegacyBindingsWithoutTouchingForeignOnes(t *testing.T) {
	root := initGitRepo(t)
	path, err := claudeSettingsPath(root, ScopeLocal)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(path, map[string]any{
		"hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": "lode hook session-start"},
			map[string]any{"type": "command", "command": "their-hook"},
		}}}},
		"statusLine": map[string]any{"type": "command", "command": "lode statusline"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := (ClaudeCode{}).InstallWithStatusLine(root, ScopeLocal); err != nil {
		t.Fatalf("InstallWithStatusLine: %v", err)
	}
	settings := readSettings(t, path)
	if got := HookCommands(settings, "SessionStart"); !slices.Equal(got, []string{"their-hook", "lode-hook session-start"}) {
		t.Fatalf("SessionStart = %v, want foreign and one direct binding", got)
	}
	if got := statusLineIn(settings); got != StatusLineCommand {
		t.Fatalf("statusLine = %q, want %q", got, StatusLineCommand)
	}
}

// Events() and boundNames() both derive from claudeBindings, so what this pins
// is that the derivation is faithful: every binding install writes is
// reachable through the event it runs and through the bound-name list, in the
// same spelling. Heartbeat is checked by hand because its fan-out to four
// native events is the part a careless edit to claudeBindings would thin out.
func TestBoundNamesMatchEvents(t *testing.T) {
	events := (ClaudeCode{}).Events()
	bound := map[string]bool{}
	for _, n := range boundNames(claudeBindings) {
		bound[n] = true
	}
	if len(bound) != len(claudeBindings) {
		t.Fatalf("boundNames(claudeBindings) = %v, want one entry per binding", boundNames(claudeBindings))
	}
	for _, b := range claudeBindings {
		if !strings.HasPrefix(b.Command, lodeHookPrefix) {
			t.Errorf("binding %s runs %q, which is not a `lode hook` command", b.Event, b.Command)
			continue
		}
		event := Event(strings.TrimPrefix(b.Command, lodeHookPrefix))
		name := nativeName(b)
		if !slices.Contains(events[event], name) {
			t.Errorf("Events()[%s] = %v, want it to name %s", event, events[event], name)
		}
		if !bound[name] {
			t.Errorf("boundNames(claudeBindings) = %v, want it to name %s", boundNames(claudeBindings), name)
		}
	}
	if got := events[Heartbeat]; len(got) != 4 {
		t.Errorf("Heartbeat natives = %v, want all four", got)
	}
}
