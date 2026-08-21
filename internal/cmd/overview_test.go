package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveDryRunPrintsTriples(t *testing.T) {
	// A minimal repo: manifest + one Go file per component is not needed for
	// layout; imports are skipped when go list fails (reported, not fatal).
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worklode"), 0o755); err != nil {
		t.Fatal(err)
	}
	man := `repo: github.com/acme/app
components:
  - iri: https://worklode.io/ns/id/component/github.com/acme/app
    name: app
    paths: ["**"]
`
	if err := os.WriteFile(filepath.Join(root, ".worklode", "components.yaml"),
		[]byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runDeriveLocal(t.Context(), root, "github.com", "acme", "app", true, nil)
	if err != nil {
		t.Fatalf("runDeriveLocal: %v", err)
	}
	if !strings.Contains(out, "id/repo/github.com/acme/app") ||
		!strings.Contains(out, "dc/terms/hasPart") {
		t.Fatalf("dry-run output missing layout triples:\n%s", out)
	}
}

func TestDeriveRequiresManifest(t *testing.T) {
	_, err := runDeriveLocal(t.Context(), t.TempDir(), "github.com", "acme", "app", true, nil)
	if err == nil || !strings.Contains(err.Error(), "components.yaml") {
		t.Fatalf("err = %v; want a missing-manifest error naming the file", err)
	}
}
