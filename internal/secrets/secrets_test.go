package secrets

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestKeystoreRoundTrip(t *testing.T) {
	keyring.MockInit() // in-memory backend; no real keychain touched

	if err := Put("WL-7", "GITHUB_TOKEN", "gh_value"); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := Fetch("WL-7", "GITHUB_TOKEN")
	if err != nil || got != "gh_value" {
		t.Fatalf("fetch = %q, %v; want gh_value", got, err)
	}
	// Scoped per task: another task sees nothing (least privilege).
	if _, err := Fetch("WL-8", "GITHUB_TOKEN"); err == nil {
		t.Fatal("cross-task fetch succeeded; want miss")
	}
	if err := Del("WL-7", "GITHUB_TOKEN"); err != nil {
		t.Fatalf("del: %v", err)
	}
	if _, err := Fetch("WL-7", "GITHUB_TOKEN"); err == nil {
		t.Fatal("fetch after delete succeeded")
	}
	// Deleting a missing item is a no-op, so purge is idempotent.
	if err := Del("WL-7", "GITHUB_TOKEN"); err != nil {
		t.Fatalf("second del: %v", err)
	}
}

func TestManifestRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, ok := LoadManifest("WL-7"); ok {
		t.Fatal("manifest exists before save")
	}
	m := Manifest{Task: "WL-7", Materialized: []string{"A_TOKEN"}, Declined: []string{"B_KEY"}, At: time.Now().UTC()}
	if err := SaveManifest(m); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := LoadManifest("WL-7")
	if !ok || got.Materialized[0] != "A_TOKEN" || got.Declined[0] != "B_KEY" {
		t.Fatalf("load = %+v, %v", got, ok)
	}
	if err := RemoveManifest("WL-7"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := LoadManifest("WL-7"); ok {
		t.Fatal("manifest survived remove")
	}
}

// TestManifestRejectsTraversingTaskID: the task id becomes a path segment, so
// an id carrying ".." would let `lode secret purge --task ../../x` unlink and
// SaveManifest write outside the secrets directory.
func TestManifestRejectsTraversingTaskID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	victim := filepath.Join(home, "victim.json")
	if err := os.WriteFile(victim, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}

	for _, id := range []string{"../../victim", "..", "WL-7/../../victim", "", "wl-7"} {
		if err := RemoveManifest(id); err == nil {
			t.Errorf("RemoveManifest(%q) = nil; want a rejection", id)
		}
		if err := SaveManifest(Manifest{Task: id}); err == nil {
			t.Errorf("SaveManifest(%q) = nil; want a rejection", id)
		}
		if _, ok := LoadManifest(id); ok {
			t.Errorf("LoadManifest(%q) = ok; want a rejection", id)
		}
		if _, err := PurgeTask(id); err == nil {
			t.Errorf("PurgeTask(%q) = nil; want a rejection", id)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("file outside the secrets directory was touched: %v", err)
	}
}

// TestKeystoreRejectsMalformedNames: the keystore is a client-side writer of
// secret names, held to the same grammar the server gates on.
func TestKeystoreRejectsMalformedNames(t *testing.T) {
	keyring.MockInit()

	for _, name := range []string{"A_TOKEN=INJECTED", "lowercase", "", "WITH SPACE"} {
		if err := Put("WL-7", name, "v"); err == nil {
			t.Errorf("Put(%q) = nil; want a rejection", name)
		}
		if _, err := Fetch("WL-7", name); err == nil {
			t.Errorf("Fetch(%q) = nil; want a rejection", name)
		}
		if err := Del("WL-7", name); err == nil {
			t.Errorf("Del(%q) = nil; want a rejection", name)
		}
	}
	// A bad task id is refused on the same paths, so no keystore item can be
	// written under a service name the manifest could never name back.
	if err := Put("../x", "A_TOKEN", "v"); err == nil {
		t.Error("Put with a traversing task id = nil; want a rejection")
	}
}

func TestPurgeTask(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())

	for _, n := range []string{"A_TOKEN", "B_KEY"} {
		if err := Put("WL-7", n, "v-"+n); err != nil {
			t.Fatalf("put %s: %v", n, err)
		}
	}
	if err := SaveManifest(Manifest{Task: "WL-7", Materialized: []string{"A_TOKEN", "B_KEY"}, At: time.Now()}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	names, err := PurgeTask("WL-7")
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("purged %v; want both names", names)
	}
	for _, n := range []string{"A_TOKEN", "B_KEY"} {
		if _, err := Fetch("WL-7", n); err == nil {
			t.Fatalf("%s survived purge", n)
		}
	}
	if _, ok := LoadManifest("WL-7"); ok {
		t.Fatal("manifest survived purge")
	}
	// No manifest ⇒ nothing to purge, not an error (idempotent release hooks).
	if names, err := PurgeTask("WL-7"); err != nil || len(names) != 0 {
		t.Fatalf("second purge = %v, %v; want empty, nil", names, err)
	}
}

func TestWriteEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".worklode", "secrets.env")
	entries := []Entry{
		{Name: "KUBECONFIG_HZDEV", Ref: "op://Infrastructure/hzdev kubeconfig/kubeconfig"},
		{Name: "GITHUB_TOKEN", Ref: "op://Employee/GitHub agent token/credential"},
	}
	if err := WriteEnvFile(path, entries); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o; want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "GITHUB_TOKEN=op://Employee/GitHub agent token/credential\n" +
		"KUBECONFIG_HZDEV=op://Infrastructure/hzdev kubeconfig/kubeconfig\n"
	if string(data) != want {
		t.Fatalf("env file:\n%s\nwant:\n%s", data, want)
	}
	if strings.Contains(string(data), "gh_value") {
		t.Fatal("env file must hold references only")
	}
}

// TestMaterializedTasks: the manifest directory is the machine's only
// inventory of materialized secrets (keyring cannot enumerate), so a
// machine-wide sweep depends on reading it exactly.
func TestMaterializedTasks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Nothing materialized yet: an empty machine, not an error.
	ids, err := MaterializedTasks()
	if err != nil {
		t.Fatalf("MaterializedTasks on an empty machine: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("MaterializedTasks on an empty machine = %v, want none", ids)
	}

	for _, id := range []string{"WL-9", "WL-7"} {
		if err := SaveManifest(Manifest{Task: id, Materialized: []string{"A_TOKEN"}, At: time.Now()}); err != nil {
			t.Fatalf("save manifest %s: %v", id, err)
		}
	}

	// The directory is under the user's control, so junk in it must be
	// skipped rather than swept: a stray file is not a task, and a name that
	// is not a valid task id must never reach a keystore or path call.
	dir := filepath.Join(home, ".cache", "worklode", "secrets")
	for name, body := range map[string]string{"notes.txt": "x", "wl-7.json": "{}", "..json": "{}"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	ids, err = MaterializedTasks()
	if err != nil {
		t.Fatalf("MaterializedTasks: %v", err)
	}
	if !slices.Equal(ids, []string{"WL-7", "WL-9"}) {
		t.Fatalf("MaterializedTasks = %v, want [WL-7 WL-9] sorted and junk-free", ids)
	}
}
