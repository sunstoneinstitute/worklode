package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ampSettings redirects Amp's config at a temp dir and returns the settings
// path and the plugin path derived from it.
func ampSettings(t *testing.T) (settings, plugin string) {
	t.Helper()
	dir := t.TempDir()
	settings = filepath.Join(dir, "settings.json")
	t.Setenv("AMP_SETTINGS_FILE", settings)
	return settings, filepath.Join(dir, "plugins", "worklode.ts")
}

// Amp is the one adapter that generates code rather than merging config: the
// install writes a TypeScript plugin, and every binding it claims has to be a
// handler in that file calling the command the table names.
func TestAmpInstallWritesPluginBindingEveryCommand(t *testing.T) {
	_, pluginPath := ampSettings(t)
	h, ok := Get("amp")
	if !ok {
		t.Fatal("amp is not registered")
	}
	hi, err := h.InstallHooks(t.TempDir(), ScopeLocal)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if hi.Path != pluginPath {
		t.Fatalf("path = %s, want %s", hi.Path, pluginPath)
	}
	if want := []string{"session.start", "agent.start", "agent.end"}; !reflect.DeepEqual(hi.Bound, want) {
		t.Fatalf("bound = %v, want %v", hi.Bound, want)
	}

	src, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	got := string(src)
	if !strings.Contains(got, ampPluginMarker) {
		t.Fatalf("plugin is missing the %q marker: %s", ampPluginMarker, got)
	}
	if !strings.Contains(got, "export default function") {
		t.Fatalf("plugin has no default export, so Amp will not load it: %s", got)
	}
	for _, b := range ampBindings {
		if !strings.Contains(got, `amp.on("`+b.Event+`"`) {
			t.Errorf("plugin registers no handler for %q: %s", b.Event, got)
		}
		if !strings.Contains(got, b.Command) {
			t.Errorf("plugin never runs %q: %s", b.Command, got)
		}
	}
}

// The Worklode events amp binds, and the two it cannot, are both part of the
// contract: session-end and worktree-enter have no Amp Plugin API event behind
// them, and the report has to say so rather than look like a partial install.
func TestAmpEventsAndCeiling(t *testing.T) {
	_, _ = ampSettings(t)
	h, _ := Get("amp")

	want := map[Event][]string{
		SessionStart: {"session.start"},
		Heartbeat:    {"agent.start", "agent.end"},
	}
	if got := h.Events(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Events() = %v, want %v", got, want)
	}

	hi, err := h.InstallHooks(t.TempDir(), ScopeLocal)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if want := []Event{SessionEnd, WorktreeEnter}; !reflect.DeepEqual(hi.Unbound, want) {
		t.Fatalf("unbound = %v, want %v", hi.Unbound, want)
	}
	if !strings.Contains(strings.Join(hi.Notes, "\n"), "session-end") {
		t.Fatalf("notes = %v; one must explain why session-end cannot be bound", hi.Notes)
	}
}

// Every command is pasted unquoted into the generated file's shell template,
// so the table may hold only the shape that survives that.
func TestAmpCommandsAreShellSafe(t *testing.T) {
	for _, b := range ampBindings {
		if !ampCommandPattern.MatchString(b.Command) {
			t.Errorf("binding %q command %q is not %s", b.Event, b.Command, ampCommandPattern)
		}
	}
}

// A second install must converge on the same bytes, not append or reorder.
func TestAmpInstallIsIdempotent(t *testing.T) {
	_, pluginPath := ampSettings(t)
	h, _ := Get("amp")
	if _, err := h.InstallHooks(t.TempDir(), ScopeLocal); err != nil {
		t.Fatalf("install: %v", err)
	}
	first, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := h.InstallHooks(t.TempDir(), ScopeLocal); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	second, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("reinstall changed the plugin:\n%s\n---\n%s", first, second)
	}
}

// Uninstall removes the generated plugin and nothing else, and a second run
// over the now-absent file is ActionNone rather than an error.
func TestAmpUninstallRemovesOnlyItsOwnPlugin(t *testing.T) {
	_, pluginPath := ampSettings(t)
	h, _ := Get("amp")

	hu, err := h.UninstallHooks(t.TempDir(), ScopeLocal)
	if err != nil || hu.Action != ActionNone {
		t.Fatalf("uninstall with nothing installed: %+v %v", hu, err)
	}
	if hu.Path != pluginPath {
		t.Fatalf("uninstall path = %s, want %s", hu.Path, pluginPath)
	}

	if _, err := h.InstallHooks(t.TempDir(), ScopeLocal); err != nil {
		t.Fatalf("install: %v", err)
	}
	// A plugin Worklode did not write, in the directory it installs into.
	foreign := filepath.Join(filepath.Dir(pluginPath), "someone-elses.ts")
	if err := os.WriteFile(foreign, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatalf("write foreign plugin: %v", err)
	}

	hu, err = h.UninstallHooks(t.TempDir(), ScopeLocal)
	if err != nil || hu.Action != ActionRemoved {
		t.Fatalf("uninstall: %+v %v", hu, err)
	}
	if _, err := os.Stat(pluginPath); !os.IsNotExist(err) {
		t.Fatalf("plugin still there after uninstall: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("uninstall touched a foreign plugin: %v", err)
	}

	if hu, err := h.UninstallHooks(t.TempDir(), ScopeLocal); err != nil || hu.Action != ActionNone {
		t.Fatalf("second uninstall: %+v %v", hu, err)
	}
}

// A `worklode.ts` without our marker is someone else's file at a name we
// happen to want. Uninstall leaves it alone; install owns the name and
// overwrites it, which is the same trade copilot's worklode.json makes.
func TestAmpUninstallKeepsAnUnmarkedWorklodeFile(t *testing.T) {
	_, pluginPath := ampSettings(t)
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const foreign = "export default function () {}\n"
	if err := os.WriteFile(pluginPath, []byte(foreign), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h, _ := Get("amp")
	hu, err := h.UninstallHooks(t.TempDir(), ScopeLocal)
	if err != nil || hu.Action != ActionNone {
		t.Fatalf("uninstall: %+v %v", hu, err)
	}
	got, err := os.ReadFile(pluginPath)
	if err != nil || string(got) != foreign {
		t.Fatalf("uninstall removed a file it did not write: %q %v", got, err)
	}
}

// The plugin is a sibling of Amp's settings file, and neither install nor
// uninstall reads or writes the settings itself — Amp's own hook config is
// unreachable from `lode hook` and stays exactly as the user left it.
func TestAmpNeverTouchesTheSettingsFile(t *testing.T) {
	settingsPath, _ := ampSettings(t)
	const seed = `{"amp.url":"https://ampcode.com"}`
	if err := os.WriteFile(settingsPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	h, _ := Get("amp")
	if _, err := h.InstallHooks(t.TempDir(), ScopeLocal); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := h.UninstallHooks(t.TempDir(), ScopeLocal); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	got, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != seed {
		t.Fatalf("settings were rewritten: %s", got)
	}
	after, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("install or uninstall bumped the settings file's mtime")
	}
}

func TestAmpDetectFollowsSettingsFile(t *testing.T) {
	path, _ := ampSettings(t)
	h, _ := Get("amp")
	if ok, err := h.Detect(t.TempDir()); err != nil || ok {
		t.Fatalf("Detect with no settings file = %v, %v", ok, err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if ok, err := h.Detect(t.TempDir()); err != nil || !ok {
		t.Fatalf("Detect with a settings file = %v, %v", ok, err)
	}
}

func TestAmpHasNoStatusLine(t *testing.T) {
	h, _ := Get("amp")
	if _, ok := h.(StatusLiner); ok {
		t.Fatal("amp implements StatusLiner; it has no status-line slot")
	}
}
