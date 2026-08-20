package harness

import (
	"os"
	"path/filepath"
	"testing"
)

// Amp's hook actions cannot run a shell command, so the adapter binds nothing
// and — decisively — writes nothing. Skills and the git heartbeat still work
// (spec 008 §4 row 2); the install report says exactly that.
func TestAmpInstallBindsNothingAndWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("AMP_SETTINGS_FILE", path)
	seed := `{"amp.url":"https://ampcode.com"}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	h, ok := Get("amp")
	if !ok {
		t.Fatal("amp is not registered")
	}
	if got := h.Events(); len(got) != 0 {
		t.Fatalf("Events() = %v; amp can bind nothing", got)
	}
	hi, err := h.InstallHooks(t.TempDir(), ScopeLocal)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if hi.Path != path {
		t.Fatalf("path = %s, want %s", hi.Path, path)
	}
	if len(hi.Bound) != 0 {
		t.Fatalf("bound = %v; want nothing", hi.Bound)
	}
	if len(hi.Unbound) != len(AllEvents) {
		t.Fatalf("unbound = %v; want every event", hi.Unbound)
	}
	for i, e := range AllEvents {
		if hi.Unbound[i] != e {
			t.Fatalf("unbound = %v; want AllEvents order %v", hi.Unbound, AllEvents)
		}
	}
	if len(hi.Notes) == 0 {
		t.Fatal("notes are empty; the report must say why nothing was bound")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != seed {
		t.Fatalf("install rewrote the settings file: %s", got)
	}
	info2, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.ModTime().Equal(info2.ModTime()) {
		t.Fatal("install bumped the settings file's mtime")
	}

	hu, err := h.UninstallHooks(t.TempDir(), ScopeLocal)
	if err != nil || hu.Action != ActionNone {
		t.Fatalf("uninstall: %+v %v", hu, err)
	}
	if hu.Path != path {
		t.Fatalf("uninstall path = %s, want %s", hu.Path, path)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != seed {
		t.Fatalf("uninstall rewrote the settings file: %s", got)
	}
}

// An absent settings file is still not created — installing amp is a no-op in
// both directions.
func TestAmpInstallCreatesNoSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "amp", "settings.json")
	t.Setenv("AMP_SETTINGS_FILE", path)
	h, _ := Get("amp")
	if _, err := h.InstallHooks(t.TempDir(), ScopeLocal); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("install created a settings file: %v", err)
	}
}

func TestAmpDetectFollowsSettingsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	t.Setenv("AMP_SETTINGS_FILE", path)
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
