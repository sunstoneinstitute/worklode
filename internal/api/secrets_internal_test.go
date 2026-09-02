package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

// TestReadTemplate covers the catalog handler's template loading without a
// store, so the path-rejection and file-read halves are checked wherever the
// suite runs — the httptest tests around them need Postgres.
func TestReadTemplate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kubeconfig-hzdev.yaml"),
		[]byte("cert: {{ CLIENT_CERT }}\n"), 0o600); err != nil {
		t.Fatalf("write template: %v", err)
	}
	e := secrets.Entry{
		Name:     "KUBECONFIG_HZDEV",
		Template: "kubeconfig-hzdev.yaml",
		Creds:    []secrets.Cred{{Placeholder: "CLIENT_CERT", Ref: "op://v/i/cert"}},
	}
	got, err := readTemplate(dir, e)
	if err != nil || got != "cert: {{ CLIENT_CERT }}\n" {
		t.Fatalf("readTemplate = %q, %v", got, err)
	}

	// The template key names a sibling key of the projected Secret, so a
	// separator or ".." is a rejection rather than a read outside the mount.
	for _, name := range []string{"../catalog.toml", "sub/t.yaml", `..\t.yaml`, ".."} {
		bad := e
		bad.Template = name
		if _, err := readTemplate(dir, bad); err == nil {
			t.Errorf("readTemplate(%q) = nil; want a rejection", name)
		} else if !strings.Contains(err.Error(), "sibling catalog key") {
			t.Errorf("readTemplate(%q) failed for the wrong reason: %v", name, err)
		}
	}

	missing := e
	missing.Template = "absent.yaml"
	if _, err := readTemplate(dir, missing); err == nil {
		t.Error("readTemplate on a missing sibling key = nil; want an error")
	}

	// A mismatch between the template and the cred set is the handler's 500.
	mismatch := e
	mismatch.Creds = append(mismatch.Creds, secrets.Cred{Placeholder: "CLIENT_KEY", Ref: "op://v/i/key"})
	if _, err := readTemplate(dir, mismatch); err == nil ||
		!strings.Contains(err.Error(), "CLIENT_KEY") {
		t.Errorf("readTemplate with an unused cred: %v; want an error naming it", err)
	}
}
