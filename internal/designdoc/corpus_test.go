package designdoc_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

func writeDoc(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const specSrc = `---
status: accepted
issued: 2026-01-01
amends:
  "#sec-1":
    - 025-documents-in-the-backbone.md#sec-2
---
# Spec 034 — Design-doc sync

## 0. Why {#sec-0}

Intro.

## 1. Scope {#sec-1}

Body.
`

const adrSrc = `---
status: draft
kind: adr
---
# ADR 7 — A decision

## 1. Decision {#sec-1}

Text.
`

func specPlanDirs(t *testing.T) (string, string) {
	t.Helper()
	specDir, planDir := t.TempDir(), t.TempDir()
	writeDoc(t, specDir, "034-design-doc-sync.md", specSrc)
	writeDoc(t, specDir, "007-a-decision.md", adrSrc)
	writeDoc(t, planDir, "2026-08-09-sync-1-foundations.md",
		"---\nstatus: draft\nimplements: docs/specs/034-design-doc-sync.md\n---\n# Part 1\n\nProse.\n")
	writeDoc(t, planDir, "2026-08-10-sync-2-store.md",
		"---\nstatus: draft\nimplements: docs/specs/034-design-doc-sync.md\n"+
			"defers:\n  - spec: docs/specs/034-design-doc-sync.md#sec-1\n"+
			"    to: docs/specs/025-documents-in-the-backbone.md\n---\n# Part 2\n\nProse.\n")
	writeDoc(t, planDir, "2026-07-01-standalone.md",
		"---\nstatus: draft\nimplements: NO-SPEC\n---\n# Standalone\n\nProse.\n")
	return specDir, planDir
}

func TestLoadSyncCorpusIdentity(t *testing.T) {
	specDir, planDir := specPlanDirs(t)
	docs, err := designdoc.LoadSyncCorpus(specDir, planDir)
	if err != nil {
		t.Fatalf("LoadSyncCorpus: %v", err)
	}
	got := map[string]string{} // filename -> kind/ordinal
	for _, d := range docs {
		got[d.Filename] = d.Kind + "/" + d.Ordinal
	}
	want := map[string]string{
		"034-design-doc-sync.md":           "spec/34",
		"007-a-decision.md":                "adr/7",
		"2026-08-09-sync-1-foundations.md": "plan/34-1",
		"2026-08-10-sync-2-store.md":       "plan/34-2",
		"2026-07-01-standalone.md":         "plan/0-1",
	}
	for f, w := range want {
		if got[f] != w {
			t.Errorf("%s: identity = %q, want %q", f, got[f], w)
		}
	}
}

func TestLoadSyncCorpusSectionsAndEdges(t *testing.T) {
	specDir, planDir := specPlanDirs(t)
	docs, err := designdoc.LoadSyncCorpus(specDir, planDir)
	if err != nil {
		t.Fatalf("LoadSyncCorpus: %v", err)
	}
	byFile := map[string]designdoc.CorpusDoc{}
	for _, d := range docs {
		byFile[d.Filename] = d
	}

	spec := byFile["034-design-doc-sync.md"]
	if spec.Title != "Spec 034 — Design-doc sync" || spec.Status != "accepted" {
		t.Errorf("spec title/status = %q/%q", spec.Title, spec.Status)
	}
	if len(spec.Sections) != 2 || spec.Sections[0].Anchor != "sec-0" ||
		spec.Sections[1].Anchor != "sec-1" || spec.Sections[1].Depth != 2 ||
		spec.Sections[1].Position != 1 {
		t.Errorf("spec sections = %+v", spec.Sections)
	}
	if len(spec.Edges) != 1 || spec.Edges[0] != (designdoc.EdgeMeta{
		SrcAnchor: "sec-1", Rel: "amends",
		Target: "025-documents-in-the-backbone.md", TargetAnchor: "sec-2",
	}) {
		t.Errorf("spec edges = %+v", spec.Edges)
	}
	if !strings.Contains(string(spec.FrontmatterJSON), `"status":"accepted"`) {
		t.Errorf("FrontmatterJSON = %s", spec.FrontmatterJSON)
	}

	plan := byFile["2026-08-09-sync-1-foundations.md"]
	if len(plan.Sections) != 0 {
		t.Errorf("plan carries sections: %+v (025 §4: plans take none)", plan.Sections)
	}
	// The fixture uses the retired `implements:` spelling; the projected edge
	// is still the canonical wl:covers (026 §6.2).
	if len(plan.Edges) != 1 || plan.Edges[0] != (designdoc.EdgeMeta{
		Rel: "covers", Target: "docs/specs/034-design-doc-sync.md",
	}) {
		t.Errorf("plan edges = %+v", plan.Edges)
	}
	noSpec := byFile["2026-07-01-standalone.md"]
	if len(noSpec.Edges) != 1 || noSpec.Edges[0].Target != "NO-SPEC" {
		t.Errorf("NO-SPEC plan edges = %+v", noSpec.Edges)
	}

	// A plan's defers entry projects to a covers-sibling edge: covers first,
	// then defers (026 §5.3) — EdgeMeta carries no owner, the same way it
	// carries no coverage level.
	deferring := byFile["2026-08-10-sync-2-store.md"]
	wantDeferring := []designdoc.EdgeMeta{
		{Rel: "covers", Target: "docs/specs/034-design-doc-sync.md"},
		{Rel: "defers", Target: "docs/specs/034-design-doc-sync.md", TargetAnchor: "sec-1"},
	}
	if len(deferring.Edges) != len(wantDeferring) {
		t.Fatalf("deferring plan edges = %+v, want %+v", deferring.Edges, wantDeferring)
	}
	for i := range wantDeferring {
		if deferring.Edges[i] != wantDeferring[i] {
			t.Errorf("deferring plan edge %d = %+v, want %+v", i, deferring.Edges[i], wantDeferring[i])
		}
	}
}

func TestLoadSyncCorpusErrors(t *testing.T) {
	cases := map[string]struct{ dir, name, content string }{
		"bad frontmatter":   {"spec", "010-bad.md", "---\nstatus: [unclosed\n---\n# T\n"},
		"no frontmatter":    {"spec", "011-none.md", "# T\n\nBody.\n"},
		"no status":         {"spec", "012-nostatus.md", "---\nissued: 2026-01-01\n---\n# T\n"},
		"no leading number": {"spec", "notes.md", "---\nstatus: draft\n---\n# T\n"},
		"no h1":             {"spec", "013-noh1.md", "---\nstatus: draft\n---\nBody only.\n"},
		"dup anchor":        {"spec", "014-dup.md", "---\nstatus: draft\n---\n# T\n\n## 1. A {#sec-1}\n\n## 2. B {#sec-1}\n"},
		"plan bad implements": {"plan", "2026-01-01-p.md",
			"---\nstatus: draft\nimplements: docs/specs/nonumber.md\n---\n# P\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			specDir, planDir := t.TempDir(), t.TempDir()
			if tc.dir == "spec" {
				writeDoc(t, specDir, tc.name, tc.content)
			} else {
				writeDoc(t, planDir, tc.name, tc.content)
			}
			if _, err := designdoc.LoadSyncCorpus(specDir, planDir); err == nil {
				t.Fatalf("LoadSyncCorpus accepted %s; want error", name)
			} else if !strings.Contains(err.Error(), tc.name) {
				t.Errorf("error %q does not name the file %s", err, tc.name)
			}
		})
	}
}

func TestLoadSyncCorpusEmptyDirsAreOptional(t *testing.T) {
	docs, err := designdoc.LoadSyncCorpus("", "")
	if err != nil || len(docs) != 0 {
		t.Fatalf("LoadSyncCorpus(\"\",\"\") = %v, %v; want empty, nil", docs, err)
	}
}

// TestLoadSyncCorpusIgnoresNonMarkdown characterizes the corpus glob's
// existing *.md-only filter: index.yaml sits alongside the spec docs (025 §16.3)
// and must never be loaded as a corpus document.
func TestLoadSyncCorpusIgnoresNonMarkdown(t *testing.T) {
	specDir := t.TempDir()
	writeDoc(t, specDir, "034-design-doc-sync.md", specSrc)
	writeDoc(t, specDir, "index.yaml", "034-design-doc-sync.md: {}\n")

	docs, err := designdoc.LoadSyncCorpus(specDir, "")
	if err != nil {
		t.Fatalf("LoadSyncCorpus: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("LoadSyncCorpus loaded %d docs, want 1: %+v", len(docs), docs)
	}
	for _, d := range docs {
		if d.Filename == "index.yaml" {
			t.Fatalf("LoadSyncCorpus loaded index.yaml as a corpus document")
		}
	}
}
