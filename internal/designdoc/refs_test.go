package designdoc

import (
	"reflect"
	"testing"
)

// refsFixture carries every relation field the enumerator walks, including the
// inverse spellings, so the order and the anchor handling are both observable.
const refsFixture = `---
status: accepted
covers:
- spec: docs/specs/026-refs.md#sec-5
  coverage: partial
  fullCoverageWith:
  - docs/plans/026-refs-2.md
- docs/specs/025-documents.md#sec-14
requires:
- docs/specs/004-execution-backbone.md
isRequiredBy:
- docs/specs/006-knowledge-graph.md
blocks:
- docs/plans/026-refs-2.md
blockedBy:
- docs/plans/026-refs-0.md
wasDerivedFrom: docs/specs/024-prior.md
amends:
  "#sec-3":
  - docs/specs/006-knowledge-graph.md#sec-5
  ".":
  - docs/specs/007-drift-and-overview.md
amendedBy:
  "#sec-1":
  - docs/specs/030-later.md#sec-2
replaces:
  "#sec-6":
  - docs/specs/006-knowledge-graph.md#sec-8
isReplacedBy:
  ".":
  - docs/specs/031-successor.md
---
# Spec 999 — Fixture
`

func refsFixtureFrontmatter(t *testing.T) *Frontmatter {
	t.Helper()
	doc, err := Parse([]byte(refsFixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc.Frontmatter
}

func TestFrontmatterRefsWalksEveryRelation(t *testing.T) {
	got := refsFixtureFrontmatter(t).Refs()
	want := []Ref{
		{Rel: "covers", Ref: "docs/specs/026-refs.md#sec-5"},
		{Rel: "covers", Ref: "docs/specs/025-documents.md#sec-14"},
		{Rel: "requires", Ref: "docs/specs/004-execution-backbone.md"},
		{Rel: "isRequiredBy", Ref: "docs/specs/006-knowledge-graph.md"},
		{Rel: "blocks", Ref: "docs/plans/026-refs-2.md"},
		{Rel: "blockedBy", Ref: "docs/plans/026-refs-0.md"},
		{Rel: "wasDerivedFrom", Ref: "docs/specs/024-prior.md"},
		// AnchorMap keys sort as written ("#sec-3" before "."), and "." — the
		// document-level subject — is "" as a source anchor, not a literal dot.
		{SrcAnchor: "sec-3", Rel: "amends", Ref: "docs/specs/006-knowledge-graph.md#sec-5"},
		{Rel: "amends", Ref: "docs/specs/007-drift-and-overview.md"},
		{SrcAnchor: "sec-1", Rel: "amendedBy", Ref: "docs/specs/030-later.md#sec-2"},
		{SrcAnchor: "sec-6", Rel: "replaces", Ref: "docs/specs/006-knowledge-graph.md#sec-8"},
		{Rel: "isReplacedBy", Ref: "docs/specs/031-successor.md"},
	}
	if len(got) != len(want) {
		t.Fatalf("Refs() returned %d refs, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].SrcAnchor != want[i].SrcAnchor || got[i].Rel != want[i].Rel || got[i].Ref != want[i].Ref {
			t.Errorf("Refs()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The covers entry rides along with its reference: it is the one relation
// carrying a level and a fullCoverageWith closure (026 §5.1).
func TestFrontmatterRefsCarriesCoverageEntry(t *testing.T) {
	got := refsFixtureFrontmatter(t).Refs()
	partial := got[0]
	if partial.Coverage == nil {
		t.Fatalf("Refs()[0].Coverage = nil, want the covers entry")
	}
	if partial.Coverage.Coverage != "partial" {
		t.Errorf("coverage level = %q, want %q", partial.Coverage.Coverage, "partial")
	}
	if want := (RefList{"docs/plans/026-refs-2.md"}); !reflect.DeepEqual(partial.Coverage.FullCoverageWith, want) {
		t.Errorf("fullCoverageWith = %v, want %v", partial.Coverage.FullCoverageWith, want)
	}
	for _, r := range got[2:] {
		if r.Coverage != nil {
			t.Errorf("rel %q carries a coverage entry, want nil", r.Rel)
		}
	}
}

// Mutating a returned entry must not reach back into the frontmatter: the
// enumerator is a read of the header, not a handle on it.
func TestFrontmatterRefsCoverageIsACopy(t *testing.T) {
	fm := refsFixtureFrontmatter(t)
	fm.Refs()[0].Coverage.Coverage = "none"
	if got := fm.CoverageEntries()[0].Coverage; got != "partial" {
		t.Errorf("frontmatter coverage level = %q after mutating the returned copy, want %q", got, "partial")
	}
}

func TestFrontmatterRefsReadsRetiredCoverageSpelling(t *testing.T) {
	doc, err := Parse([]byte("---\nimplements:\n- docs/specs/025-documents.md\n---\n# X\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := doc.Frontmatter.Refs()
	if len(got) != 1 || got[0].Rel != "covers" || got[0].Ref != "docs/specs/025-documents.md" {
		t.Fatalf("Refs() = %+v, want one covers ref", got)
	}
}

// Blank and whitespace-only references name no target, so they are dropped
// rather than enumerated as an empty edge.
func TestFrontmatterRefsDropsBlankReferences(t *testing.T) {
	doc, err := Parse([]byte("---\nrequires:\n- \"  \"\n- \"  docs/specs/004-x.md \"\ncovers:\n- coverage: full\n---\n# X\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := doc.Frontmatter.Refs()
	want := []Ref{{Rel: "requires", Ref: "docs/specs/004-x.md"}}
	if len(got) != 1 || got[0].Rel != want[0].Rel || got[0].Ref != want[0].Ref {
		t.Fatalf("Refs() = %+v, want %+v", got, want)
	}
}

// defersFixture pairs a covers entry with two defers entries, so ordering
// (covers before defers before requires) and the Deferral payload are both
// observable.
const defersFixture = `---
status: accepted
covers:
- docs/specs/025-documents.md#sec-11
defers:
- spec: docs/specs/025-documents.md#sec-12
  to: docs/specs/006-knowledge-graph.md
- spec: docs/specs/025-documents.md#sec-13
  to: docs/plans/2026-08-10-successor.md
requires:
- docs/specs/004-execution-backbone.md
---
# A plan
`

func TestFrontmatterRefsWalksDefers(t *testing.T) {
	doc, err := Parse([]byte(defersFixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := doc.Frontmatter.Refs()
	want := []Ref{
		{Rel: "covers", Ref: "docs/specs/025-documents.md#sec-11"},
		{Rel: "defers", Ref: "docs/specs/025-documents.md#sec-12"},
		{Rel: "defers", Ref: "docs/specs/025-documents.md#sec-13"},
		{Rel: "requires", Ref: "docs/specs/004-execution-backbone.md"},
	}
	if len(got) != len(want) {
		t.Fatalf("Refs() returned %d refs, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].SrcAnchor != want[i].SrcAnchor || got[i].Rel != want[i].Rel || got[i].Ref != want[i].Ref {
			t.Errorf("Refs()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The defers entry rides along with its reference: it is the one relation
// carrying the named owner (026 §5.3).
func TestFrontmatterRefsCarriesDeferralEntry(t *testing.T) {
	doc, err := Parse([]byte(defersFixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := doc.Frontmatter.Refs()
	defers := got[1]
	if defers.Deferral == nil {
		t.Fatalf("Refs()[1].Deferral = nil, want the defers entry")
	}
	if defers.SrcAnchor != "" {
		t.Errorf("SrcAnchor = %q, want empty", defers.SrcAnchor)
	}
	if want := "docs/specs/006-knowledge-graph.md"; defers.Deferral.To != want {
		t.Errorf("Deferral.To = %q, want %q", defers.Deferral.To, want)
	}
	for _, r := range got {
		if r.Rel != "defers" && r.Deferral != nil {
			t.Errorf("rel %q carries a deferral entry, want nil", r.Rel)
		}
	}
}

func TestFrontmatterRefsForDefers(t *testing.T) {
	doc, err := Parse([]byte(defersFixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := doc.Frontmatter.RefsFor("defers")
	want := []string{
		"docs/specs/025-documents.md#sec-12",
		"docs/specs/025-documents.md#sec-13",
	}
	if len(got) != len(want) {
		t.Fatalf("RefsFor() returned %d refs, want %d: %+v", len(got), len(want), got)
	}
	for i, r := range got {
		if r.Ref != want[i] {
			t.Errorf("RefsFor()[%d].Ref = %q, want %q", i, r.Ref, want[i])
		}
	}
}

func TestFrontmatterRefsFor(t *testing.T) {
	got := refsFixtureFrontmatter(t).RefsFor("requires", "amends")
	want := []string{
		"docs/specs/004-execution-backbone.md",
		"docs/specs/006-knowledge-graph.md#sec-5",
		"docs/specs/007-drift-and-overview.md",
	}
	if len(got) != len(want) {
		t.Fatalf("RefsFor() returned %d refs, want %d: %+v", len(got), len(want), got)
	}
	for i, r := range got {
		if r.Ref != want[i] {
			t.Errorf("RefsFor()[%d].Ref = %q, want %q", i, r.Ref, want[i])
		}
	}
}

func TestFrontmatterRefsNil(t *testing.T) {
	var fm *Frontmatter
	if got := fm.Refs(); got != nil {
		t.Errorf("(*Frontmatter)(nil).Refs() = %+v, want nil", got)
	}
	if got := fm.RefsFor("covers"); got != nil {
		t.Errorf("(*Frontmatter)(nil).RefsFor() = %+v, want nil", got)
	}
}

// ActingRels is the set store and the importer record; the inverse spellings
// are read back off the acting row (025 §14) and must stay out of it.
func TestActingRelsExcludesInverseSpellings(t *testing.T) {
	for _, rel := range ActingRels {
		switch rel {
		case "isRequiredBy", "blockedBy", "amendedBy", "isReplacedBy":
			t.Errorf("ActingRels contains inverse spelling %q", rel)
		}
	}
	for _, rel := range []string{"covers", "defers", "requires", "blocks", "wasDerivedFrom", "amends", "replaces"} {
		if !contains(ActingRels, rel) {
			t.Errorf("ActingRels is missing %q", rel)
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
