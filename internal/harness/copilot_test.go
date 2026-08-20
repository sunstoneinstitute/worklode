package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// copilotHandlers decodes the flat (no matcher-group layer) handler list one
// event carries in a Copilot hooks file.
func copilotHandlers(t *testing.T, path, event string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg struct {
		Version int                         `json:"version"`
		Hooks   map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	// Copilot ignores a hooks file without it, so the version is load-bearing.
	if cfg.Version != 1 {
		t.Fatalf("version = %d, want 1: %s", cfg.Version, data)
	}
	handlers, ok := cfg.Hooks[event]
	if !ok {
		t.Fatalf("event %q missing from %s", event, data)
	}
	return handlers
}

func TestCopilotInstallWritesOwnedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	hooksDir := filepath.Join(home, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// The filename is arbitrary, so foreign siblings are the coexistence case.
	foreign := filepath.Join(hooksDir, "theirs.json")
	if err := os.WriteFile(foreign, []byte(`{"keep":true}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	h, ok := Get("copilot")
	if !ok {
		t.Fatal("copilot is not registered")
	}
	hi, err := h.InstallHooks(t.TempDir(), ScopeLocal)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if want := filepath.Join(hooksDir, "worklode.json"); hi.Path != want {
		t.Fatalf("path = %s, want %s", hi.Path, want)
	}
	if len(hi.Unbound) != 1 || hi.Unbound[0] != WorktreeEnter {
		t.Fatalf("unbound = %v; want [worktree-enter]", hi.Unbound)
	}
	if len(hi.Bound) != 4 {
		t.Fatalf("bound = %v; want the four copilot events", hi.Bound)
	}

	for event, want := range map[string]Event{
		"sessionStart": SessionStart,
		"sessionEnd":   SessionEnd,
		"agentStop":    Heartbeat,
		"subagentStop": Heartbeat,
	} {
		handlers := copilotHandlers(t, hi.Path, event)
		if len(handlers) != 1 {
			t.Fatalf("%s handlers = %v; want one", event, handlers)
		}
		if got := handlers[0]["type"]; got != "command" {
			t.Fatalf("%s type = %v, want command", event, got)
		}
		wantCmd := "lode hook " + string(want) + " --harness copilot"
		if got := handlers[0]["command"]; got != wantCmd {
			t.Fatalf("%s command = %v, want %q", event, got, wantCmd)
		}
		// `command` covers both platforms; a bash key would leave Windows out.
		if _, ok := handlers[0]["bash"]; ok {
			t.Fatalf("%s handler has a bash key: %v", event, handlers[0])
		}
	}

	// Re-install converges byte-identically.
	before, err := os.ReadFile(hi.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := h.InstallHooks(t.TempDir(), ScopeLocal); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	after, err := os.ReadFile(hi.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("reinstall did not converge:\n%s\n---\n%s", before, after)
	}

	// The foreign sibling is untouched.
	got, err := os.ReadFile(foreign)
	if err != nil {
		t.Fatalf("read foreign: %v", err)
	}
	if string(got) != `{"keep":true}` {
		t.Fatalf("foreign file changed: %s", got)
	}

	// Uninstall deletes only our file; a second one is a no-op.
	hu, err := h.UninstallHooks(t.TempDir(), ScopeLocal)
	if err != nil || hu.Action != ActionRemoved {
		t.Fatalf("uninstall: %+v %v", hu, err)
	}
	if _, err := os.Stat(hi.Path); !os.IsNotExist(err) {
		t.Fatalf("worklode.json survived uninstall: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("uninstall removed a foreign file: %v", err)
	}
	hu, err = h.UninstallHooks(t.TempDir(), ScopeLocal)
	if err != nil || hu.Action != ActionNone {
		t.Fatalf("second uninstall: %+v %v", hu, err)
	}
}

// ScopeProject writes the committed repo layer, mirroring Claude Code's split.
func TestCopilotProjectScopeWritesRepoHooks(t *testing.T) {
	t.Setenv("COPILOT_HOME", t.TempDir())
	repo := t.TempDir()
	h, _ := Get("copilot")
	hi, err := h.InstallHooks(repo, ScopeProject)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if want := filepath.Join(repo, ".github", "hooks", "worklode.json"); hi.Path != want {
		t.Fatalf("path = %s, want %s", hi.Path, want)
	}
	if _, err := os.Stat(hi.Path); err != nil {
		t.Fatalf("project hooks file: %v", err)
	}
	copilotHandlers(t, hi.Path, "sessionStart")
}

func TestCopilotUnknownScopeErrors(t *testing.T) {
	t.Setenv("COPILOT_HOME", t.TempDir())
	h, _ := Get("copilot")
	if _, err := h.InstallHooks(t.TempDir(), "global"); err == nil {
		t.Fatal("unknown scope was accepted")
	}
}

// Detection fires on the personal directory or on a repo carrying Copilot's
// instruction file.
func TestCopilotDetect(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", filepath.Join(home, "absent"))
	repo := t.TempDir()
	h, _ := Get("copilot")
	if ok, err := h.Detect(repo); err != nil || ok {
		t.Fatalf("Detect with nothing configured = %v, %v", ok, err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".github"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".github", "copilot-instructions.md"), nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if ok, err := h.Detect(repo); err != nil || !ok {
		t.Fatalf("Detect with copilot-instructions.md = %v, %v", ok, err)
	}
	t.Setenv("COPILOT_HOME", home)
	if ok, err := h.Detect(t.TempDir()); err != nil || !ok {
		t.Fatalf("Detect with a copilot home = %v, %v", ok, err)
	}
}
