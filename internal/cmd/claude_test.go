package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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

func TestClaudeInstallScopes(t *testing.T) {
	root := t.TempDir()

	local, err := claudeSettingsPath(root, scopeLocal)
	if err != nil {
		t.Fatalf("local path: %v", err)
	}
	if want := filepath.Join(root, ".claude", "settings.local.json"); local != want {
		t.Fatalf("local scope path: got %s, want %s", local, want)
	}
	project, err := claudeSettingsPath(root, scopeProject)
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

	if err := uninstallClaudeHooks(path); err != nil {
		t.Fatalf("uninstall: %v", err)
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

func TestClaudeUninstallWithNoSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	if err := uninstallClaudeHooks(path); err != nil {
		t.Fatalf("uninstall with no settings file: %v, want nil", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("uninstall created a settings file")
	}
}
