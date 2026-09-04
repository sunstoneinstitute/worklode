package harness

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codexHooks decodes the hooks object out of the file at path.
func codexHooks(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	hooks, ok := cfg["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks missing from %s: %s", path, data)
	}
	return hooks
}

// commandsForEvent flattens one event's matcher groups into the command
// strings they run — Codex uses Claude Code's three-level shape.
func commandsForEvent(t *testing.T, hooks map[string]any, event string) []string {
	t.Helper()
	groups, ok := hooks[event].([]any)
	if !ok {
		t.Fatalf("event %q missing or not a group list: %#v", event, hooks[event])
	}
	var out []string
	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			t.Fatalf("group in %q is not an object: %#v", event, g)
		}
		entries, ok := group["hooks"].([]any)
		if !ok {
			t.Fatalf("group in %q has no handler list: %#v", event, group)
		}
		for _, e := range entries {
			entry, ok := e.(map[string]any)
			if !ok {
				t.Fatalf("handler in %q is not an object: %#v", event, e)
			}
			out = append(out, entry["command"].(string))
		}
	}
	return out
}

func TestCodexInstallUninstallRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	// Foreign content that must survive: a top-level key Codex owns and a
	// foreign handler on an event we also bind.
	seed := `{"description":"theirs","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"their-hook"}]}]}}`
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Foreign content in the sibling config.toml that must also survive:
	// a top-level key Codex owns and an unrelated key already under [otel].
	configSeed := "model = \"gpt-5.6-sol\"\n[otel]\nenvironment = \"dev\"\n"
	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte(configSeed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	h, ok := Get("codex")
	if !ok {
		t.Fatal("codex is not registered")
	}
	hi, err := h.InstallHooks(t.TempDir(), ScopeLocal)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if want := filepath.Join(home, "hooks.json"); hi.Path != want {
		t.Fatalf("path = %s, want %s", hi.Path, want)
	}
	if len(hi.Unbound) != 1 || hi.Unbound[0] != WorktreeEnter {
		t.Fatalf("unbound = %v; want [worktree-enter]", hi.Unbound)
	}
	if len(hi.Bound) != 4 {
		t.Fatalf("bound = %v; want the four codex events", hi.Bound)
	}
	// A codex hook is inert until the user approves it, so the install report
	// has to say so, and the second file's path and action must appear too --
	// hooks.json alone does not tell a reader that config.toml changed.
	if len(hi.Notes) != 2 || !strings.Contains(hi.Notes[0], "/hooks") {
		t.Fatalf("notes = %v; want the trust-gate advice first", hi.Notes)
	}
	if !strings.Contains(hi.Notes[1], configPath) || !strings.Contains(hi.Notes[1], ActionInstalled) {
		t.Fatalf("notes[1] = %q; want it to name %s and %s", hi.Notes[1], configPath, ActionInstalled)
	}

	// The telemetry config is correct and every foreign key survived.
	cfg := readCodexConfig(t, configPath)
	if cfg["model"] != "gpt-5.6-sol" {
		t.Fatalf("foreign top-level config key lost: %#v", cfg)
	}
	otel := cfg["otel"].(map[string]any)
	if otel["environment"] != "dev" {
		t.Fatalf("foreign otel key lost: %#v", otel)
	}
	if otel["log_user_prompt"] != false {
		t.Fatalf("log_user_prompt = %v, want false", otel["log_user_prompt"])
	}
	exporter := otel["exporter"].(map[string]any)
	grpc := exporter["otlp-grpc"].(map[string]any)
	if grpc["endpoint"] != "http://127.0.0.1:4317" {
		t.Fatalf("exporter endpoint = %v", grpc["endpoint"])
	}

	// Re-run converges: same bytes, both files.
	before, err := os.ReadFile(hi.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	beforeConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if _, err := h.InstallHooks(t.TempDir(), ScopeLocal); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	after, err := os.ReadFile(hi.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("reinstall did not converge:\n%s\n---\n%s", before, after)
	}
	afterConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !bytes.Equal(beforeConfig, afterConfig) {
		t.Fatalf("reinstall did not converge config.toml:\n%s\n---\n%s", beforeConfig, afterConfig)
	}

	var hooksJSON map[string]any
	if err := json.Unmarshal(after, &hooksJSON); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if hooksJSON["description"] != "theirs" {
		t.Fatalf("foreign top-level key lost: %s", after)
	}
	hooks := codexHooks(t, hi.Path)
	for event, wantEvent := range map[string]Event{
		"SessionStart": SessionStart,
		"SessionEnd":   SessionEnd,
		"Stop":         Heartbeat,
		"SubagentStop": Heartbeat,
	} {
		var ours []string
		for _, c := range commandsForEvent(t, hooks, event) {
			if strings.HasPrefix(c, lodeHookPrefix) {
				ours = append(ours, c)
			}
		}
		want := "lode-hook " + string(wantEvent) + " --harness codex"
		if len(ours) != 1 || ours[0] != want {
			t.Fatalf("%s commands = %v; want exactly [%q]", event, ours, want)
		}
	}
	if got := commandsForEvent(t, hooks, "SessionStart"); len(got) != 2 || got[0] != "their-hook" {
		t.Fatalf("SessionStart = %v; want the foreign hook kept alongside ours", got)
	}

	// Uninstall restores the seed semantically: the foreign entry alone.
	hu, err := h.UninstallHooks(t.TempDir(), ScopeLocal)
	if err != nil || hu.Action != ActionRemoved {
		t.Fatalf("uninstall: %+v %v", hu, err)
	}
	hooks = codexHooks(t, hi.Path)
	if len(hooks) != 1 {
		t.Fatalf("hooks after uninstall = %#v; want only the foreign event", hooks)
	}
	if got := commandsForEvent(t, hooks, "SessionStart"); len(got) != 1 || got[0] != "their-hook" {
		t.Fatalf("SessionStart after uninstall = %v", got)
	}

	// Uninstall also removed Worklode's telemetry from config.toml, safely:
	// the foreign top-level key and the foreign otel key both survive, and
	// the otel table is gone rather than left empty.
	cfgAfter := readCodexConfig(t, configPath)
	if cfgAfter["model"] != "gpt-5.6-sol" {
		t.Fatalf("foreign top-level config key lost on uninstall: %#v", cfgAfter)
	}
	otelAfter, ok := cfgAfter["otel"].(map[string]any)
	if !ok {
		t.Fatalf("foreign otel key lost on uninstall: %#v", cfgAfter)
	}
	if otelAfter["environment"] != "dev" {
		t.Fatalf("foreign otel key changed on uninstall: %#v", otelAfter)
	}
	if _, ok := otelAfter["log_user_prompt"]; ok {
		t.Fatalf("log_user_prompt not removed on uninstall: %#v", otelAfter)
	}
	if _, ok := otelAfter["exporter"]; ok {
		t.Fatalf("exporter not removed on uninstall: %#v", otelAfter)
	}

	// A second uninstall is a no-op and must not rewrite either file.
	stripped, err := os.ReadFile(hi.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	strippedConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	info, err := os.Stat(hi.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	hu, err = h.UninstallHooks(t.TempDir(), ScopeLocal)
	if err != nil || hu.Action != ActionNone {
		t.Fatalf("second uninstall: %+v %v", hu, err)
	}
	again, err := os.ReadFile(hi.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(stripped, again) {
		t.Fatal("no-op uninstall rewrote the file")
	}
	againConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !bytes.Equal(strippedConfig, againConfig) {
		t.Fatal("no-op uninstall rewrote config.toml")
	}
	info2, err := os.Stat(hi.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.ModTime().Equal(info2.ModTime()) {
		t.Fatal("no-op uninstall bumped the file's mtime")
	}
}

// A user who repointed the exporter elsewhere keeps it: uninstall only
// removes values that still match what Worklode wrote.
func TestCodexUninstallKeepsUserModifiedExporter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	h, _ := Get("codex")
	if _, err := h.InstallHooks(t.TempDir(), ScopeLocal); err != nil {
		t.Fatalf("install: %v", err)
	}
	configPath := filepath.Join(home, "config.toml")
	if _, err := installCodexTelemetry(configPath); err != nil {
		t.Fatalf("seed telemetry: %v", err)
	}
	cfg := readCodexConfig(t, configPath)
	otel := cfg["otel"].(map[string]any)
	otel["exporter"] = map[string]any{"otlp-http": map[string]any{"endpoint": "https://example.com"}}
	if err := writeCodexConfigFile(configPath, cfg); err != nil {
		t.Fatalf("reseed: %v", err)
	}

	if _, err := h.UninstallHooks(t.TempDir(), ScopeLocal); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	after := readCodexConfig(t, configPath)
	otelAfter, ok := after["otel"].(map[string]any)
	if !ok {
		t.Fatalf("user-modified exporter's otel table was removed: %#v", after)
	}
	exporter, ok := otelAfter["exporter"].(map[string]any)
	if !ok {
		t.Fatalf("user-modified exporter was removed: %#v", otelAfter)
	}
	httpExp, ok := exporter["otlp-http"].(map[string]any)
	if !ok || httpExp["endpoint"] != "https://example.com" {
		t.Fatalf("user-modified exporter changed: %#v", exporter)
	}
	if _, ok := otelAfter["log_user_prompt"]; ok {
		t.Fatalf("log_user_prompt (still Worklode's value) was not removed: %#v", otelAfter)
	}
}

// A file that exists but does not parse is returned as an error, never
// overwritten (spec 024 acceptance 6).
func TestCodexRefusesUnroundtrippableFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "hooks.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h, _ := Get("codex")
	if _, err := h.InstallHooks(t.TempDir(), ScopeLocal); err == nil {
		t.Fatal("unparseable config was accepted")
	}
	if _, err := h.UninstallHooks(t.TempDir(), ScopeLocal); err == nil {
		t.Fatal("unparseable config was accepted by uninstall")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "not json" {
		t.Fatalf("unparseable config was rewritten: %s", data)
	}
}

// Both scopes write the user-level file: project-level .codex/hooks.json is
// silently ignored inside a git worktree (openai/codex#27133), so writing
// there would never fire for a Worklode task; the `lode-hook` guard, not the
// config layer, is what scopes behaviour to Worklode worktrees.
func TestCodexScopesShareOneFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	h, _ := Get("codex")
	local, err := h.InstallHooks(t.TempDir(), ScopeLocal)
	if err != nil {
		t.Fatalf("install local: %v", err)
	}
	project, err := h.InstallHooks(t.TempDir(), ScopeProject)
	if err != nil {
		t.Fatalf("install project: %v", err)
	}
	if local.Path != project.Path {
		t.Fatalf("paths differ: %s vs %s", local.Path, project.Path)
	}
	// The telemetry config.toml is user-level too, not reinstalled per
	// scope -- both notes must name the exact same path.
	wantConfig := filepath.Join(home, "config.toml")
	if !strings.Contains(local.Notes[1], wantConfig) || !strings.Contains(project.Notes[1], wantConfig) {
		t.Fatalf("telemetry notes = %q / %q; want both to name %s", local.Notes[1], project.Notes[1], wantConfig)
	}
}

func TestCodexDetectFollowsCodexHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(dir, "absent"))
	h, _ := Get("codex")
	if ok, err := h.Detect(t.TempDir()); err != nil || ok {
		t.Fatalf("Detect with no codex home = %v, %v", ok, err)
	}
	t.Setenv("CODEX_HOME", dir)
	if ok, err := h.Detect(t.TempDir()); err != nil || !ok {
		t.Fatalf("Detect with a codex home = %v, %v", ok, err)
	}
}
