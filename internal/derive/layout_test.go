package derive_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

// writeTree creates the given empty files under a temp root.
func writeTree(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return root
}

func TestLayoutTriplesMultiComponent(t *testing.T) {
	m, err := manifest.Parse([]byte(importsManifest)) // fixture from imports_test.go
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	root := writeTree(t,
		"cmd/ingest/main.go", "internal/ingest/ingest.go",
		"internal/graph/graph.go",
		"scripts/build.sh", "README.md",
	)
	doc, err := derive.LayoutTriples(root, "github.com", "sunstoneinstitute", "research-stack", m)
	if err != nil {
		t.Fatalf("LayoutTriples: %v", err)
	}
	got := string(doc)
	repo := "<https://worklode.io/ns/id/repo/github.com/sunstoneinstitute/research-stack>"
	for _, line := range []string{
		repo + " <http://purl.org/dc/terms/hasPart> <https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest> .",
		repo + " <http://purl.org/dc/terms/hasPart> <https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/graphsrv> .",
		"<https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Component> .",
		repo + ` <https://worklode.io/ns/ontology#unmatchedPath> "README.md" .`,
		repo + ` <https://worklode.io/ns/ontology#unmatchedPath> "scripts" .`,
	} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, got)
		}
	}
	if strings.Contains(got, `"cmd"`) || strings.Contains(got, `"internal"`) {
		t.Errorf("matched trees reported as gaps:\n%s", got)
	}
}

func TestLayoutTriplesSkipsDotGit(t *testing.T) {
	m, _ := manifest.Parse([]byte(importsManifest))
	root := writeTree(t, ".git/config", ".worklode/components.yaml", "internal/ingest/i.go")
	doc, err := derive.LayoutTriples(root, "github.com", "o", "r", m)
	if err != nil {
		t.Fatalf("LayoutTriples: %v", err)
	}
	if strings.Contains(string(doc), ".git") || strings.Contains(string(doc), ".worklode") {
		t.Fatalf("dotdirs reported as gaps:\n%s", doc)
	}
}
