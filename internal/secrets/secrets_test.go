package secrets

import (
	"os"
	"path/filepath"
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
