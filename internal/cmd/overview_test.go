package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
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

	out, err := runDeriveLocal(t.Context(), root, "github.com", "acme", "app", true, nil, derive.Options{})
	if err != nil {
		t.Fatalf("runDeriveLocal: %v", err)
	}
	if !strings.Contains(out, "id/repo/github.com/acme/app") ||
		!strings.Contains(out, "dc/terms/hasPart") {
		t.Fatalf("dry-run output missing layout triples:\n%s", out)
	}
}

func TestDeriveRequiresManifest(t *testing.T) {
	_, err := runDeriveLocal(t.Context(), t.TempDir(), "github.com", "acme", "app", true, nil, derive.Options{})
	if err == nil || !strings.Contains(err.Error(), "components.yaml") {
		t.Fatalf("err = %v; want a missing-manifest error naming the file", err)
	}
}

// TestDeriveDryRunNamesAnEmptyDocument: a whole-repo component drops every
// import edge as intra-component, so go-imports comes out empty — worklode's
// own case. The dry run must say so rather than print a bare graph header
// with nothing beneath it, which reads as truncated output.
func TestDeriveDryRunNamesAnEmptyDocument(t *testing.T) {
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
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(".worklode", "components.yaml"), man)
	write("go.mod", "module example.com/app\n\ngo 1.24\n")
	write("main.go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println() }\n")

	out, err := runDeriveLocal(t.Context(), root, "github.com", "acme", "app", true, nil, derive.Options{})
	if err != nil {
		t.Fatalf("runDeriveLocal: %v", err)
	}
	if strings.Contains(out, "go-imports skipped:") {
		t.Skipf("go list unavailable in this environment:\n%s", out)
	}
	if !strings.Contains(out, "observed/go-imports") || !strings.Contains(out, "# (empty:") {
		t.Fatalf("dry-run output must name the empty go-imports document:\n%s", out)
	}
}
