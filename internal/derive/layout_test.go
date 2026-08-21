package derive_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

// writeTree creates the given empty files under a temp root.
func writeTree(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range paths {
		writeFile(t, root, p, "")
	}
	return root
}

// writeFile creates one file under root, parent directories included.
func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// initGitRepo turns root into a git repo and stages add — the files the
// deriver should then see as tracked. Staging is enough: `git ls-files` reads
// the index, so no commit (and no committer identity) is needed.
func initGitRepo(t *testing.T, root string, add ...string) {
	t.Helper()
	if err := gitexec.Run(root, "init", "-q"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	args := append([]string{"add", "--"}, add...)
	if err := gitexec.Run(root, args...); err != nil {
		t.Fatalf("git add: %v", err)
	}
}

// TestLayoutTriplesTrackedFilesOnly is the determinism contract of spec 007
// §2 ("Same inputs -> same triples"): running a build must not change the
// derived document. Untracked files — ignored build output above all — are
// not the repo's layout, so they are neither a coverage gap nor an input to
// the content hash Run short-circuits on.
func TestLayoutTriplesTrackedFilesOnly(t *testing.T) {
	m, err := manifest.Parse([]byte(importsManifest))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	root := writeTree(t,
		"cmd/ingest/main.go", "internal/ingest/ingest.go",
		"scripts/build.sh", ".github/workflows/ci.yml",
	)
	writeFile(t, root, ".gitignore", "bin/\n*.db\n")
	initGitRepo(t, root, "cmd", "internal", "scripts", ".github", ".gitignore")

	ctx := context.Background()
	before, err := derive.LayoutTriples(ctx, root, "github.com", "o", "r", m)
	if err != nil {
		t.Fatalf("LayoutTriples: %v", err)
	}
	// A tracked path no component claims must still be reported, or the rest
	// of this test passes vacuously.
	if !strings.Contains(string(before), `"scripts"`) {
		t.Fatalf("tracked unmatched path not reported:\n%s", before)
	}

	// Simulate a build plus an in-progress edit: ignored output, an ignored
	// file matched by a glob rather than a directory, and an untracked file
	// git would happily report as untracked.
	writeFile(t, root, "bin/lode", "ELF")
	writeFile(t, root, "local.db", "")
	writeFile(t, root, "NOTES.md", "wip")

	after, err := derive.LayoutTriples(ctx, root, "github.com", "o", "r", m)
	if err != nil {
		t.Fatalf("LayoutTriples after build: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("document changed after a build (deriver is not deterministic)\nbefore:\n%s\nafter:\n%s", before, after)
	}
	for _, unwanted := range []string{`"bin"`, `"local.db"`, `"NOTES.md"`} {
		if strings.Contains(string(after), unwanted) {
			t.Errorf("untracked path %s reported as a coverage gap:\n%s", unwanted, after)
		}
	}
	if strings.Contains(string(after), ".git") {
		t.Errorf("tracked dot-paths reported as gaps:\n%s", after)
	}
}

// TestLayoutTriplesMultiComponent covers the walk fallback: a plain directory
// that is not inside a git work tree has no tracked set, so every file counts.
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
	doc, err := derive.LayoutTriples(context.Background(), root, "github.com", "sunstoneinstitute", "research-stack", m)
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
	doc, err := derive.LayoutTriples(context.Background(), root, "github.com", "o", "r", m)
	if err != nil {
		t.Fatalf("LayoutTriples: %v", err)
	}
	if strings.Contains(string(doc), ".git") || strings.Contains(string(doc), ".worklode") {
		t.Fatalf("dotdirs reported as gaps:\n%s", doc)
	}
}
