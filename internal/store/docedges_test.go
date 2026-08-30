package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestDocSchemaBlocksEdgeWithAnchorViolatesCheck(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	seedDocsProject(t, s)

	fromID, err := insertDoc(t, s, "plan", 1, "plan-a")
	if err != nil {
		t.Fatalf("insert from doc: %v", err)
	}
	toID, err := insertDoc(t, s, "plan", 2, "plan-b")
	if err != nil {
		t.Fatalf("insert to doc: %v", err)
	}

	ctx := context.Background()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO doc_edges (from_doc, from_anchor, type, to_doc, declared_by)
		 VALUES ($1, 'sec-1', 'blocks', $2, $1)`,
		fromID, toID)
	if err == nil {
		t.Fatal("expected CHECK violation, got nil error")
	}
	if !isCheckViolationOn(err, "doc_edges_check1") {
		t.Fatalf("expected doc_edges_check1 CHECK violation, got: %v", err)
	}
}

func TestDocSchemaCoversEdgeSucceeds(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	seedDocsProject(t, s)

	planID, err := insertDoc(t, s, "plan", 1, "plan-a")
	if err != nil {
		t.Fatalf("insert plan doc: %v", err)
	}
	specID, err := insertDoc(t, s, "spec", 25, "documents-in-the-backbone")
	if err != nil {
		t.Fatalf("insert spec doc: %v", err)
	}

	ctx := context.Background()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO doc_edges (from_doc, type, to_doc, to_anchor, coverage, declared_by)
		 VALUES ($1, 'covers', $2, 'sec-5', 'full', $1)`,
		planID, specID)
	if err != nil {
		t.Fatalf("insert covers edge: %v", err)
	}
}

// TestResolveDocRef covers the ref grammar the doc verbs take: an id, a slug,
// a slug nobody holds, and a slug two projects hold.
func TestResolveDocRef(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	got, err := s.ResolveDocRef(t.Context(), strconv.FormatInt(spec.ID, 10))
	if err != nil {
		t.Fatalf("ResolveDocRef(id): %v", err)
	}
	if got.ID != spec.ID {
		t.Errorf("ResolveDocRef(id) = %d, want %d", got.ID, spec.ID)
	}
	got, err = s.ResolveDocRef(t.Context(), "025-x")
	if err != nil {
		t.Fatalf("ResolveDocRef(slug): %v", err)
	}
	if got.ID != spec.ID {
		t.Errorf("ResolveDocRef(slug) = %d, want %d", got.ID, spec.ID)
	}
	if _, err := s.ResolveDocRef(t.Context(), "no-such-slug"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResolveDocRef(unmatched) = %v, want ErrNotFound", err)
	}

	// Slugs are unique per project, not globally, so the same slug in a
	// second project is ambiguous rather than resolvable.
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO projects (id, name, key) VALUES ('p2','P2','P2')`); err != nil {
		t.Fatal(err)
	}
	mustCreateDoc(t, s, DocInput{
		Project: "p2", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	_, err = s.ResolveDocRef(t.Context(), "025-x")
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "025-x") {
		t.Fatalf("ResolveDocRef(ambiguous) = %v, want ErrInvalidInput naming the slug", err)
	}
}

// TestResolveDocRefFallsBackToTombstones pins the half `lode doc undelete
// <slug>` depends on: a deleted document has left every list, so a resolver
// that stopped at the live rows could not name it (044 §4). A live document
// with that slug still wins.
func TestResolveDocRefFallsBackToTombstones(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	gone := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	if err := deleteDoc(t, s, gone.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteDoc: %v", err)
	}

	got, err := s.ResolveDocRef(t.Context(), "025-x")
	if err != nil {
		t.Fatalf("ResolveDocRef(tombstoned slug): %v", err)
	}
	if got.ID != gone.ID || got.Tombstone == nil {
		t.Fatalf("ResolveDocRef(tombstoned slug) = %+v, want the tombstoned %d", got, gone.ID)
	}

	// A live document reusing the slug shadows the tombstone rather than
	// making the ref ambiguous.
	live := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 26, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	got, err = s.ResolveDocRef(t.Context(), "025-x")
	if err != nil {
		t.Fatalf("ResolveDocRef(live over tombstone): %v", err)
	}
	if got.ID != live.ID {
		t.Fatalf("ResolveDocRef(live over tombstone) = %d, want the live %d", got.ID, live.ID)
	}
}

// TestReplaceDocEdges is the corpus import's second pass: a frontmatter
// reference that resolved to nothing when the document was created becomes a
// real edge once its target exists. Nothing authored moves — and unlike
// UpdateDocBody it runs at accepted, because no anchor is being restated.
func TestReplaceDocEdges(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	// Accepted at creation, the state the importer puts a spent plan in.
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "025-part-2", Body: planBody,
		CreatedBy: "stig", Status: "accepted",
	})
	for _, e := range docEdges(t, s, plan.ID) {
		if e.ToDoc != 0 {
			t.Fatalf("edge %+v resolved before its target existed", e)
		}
	}

	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-documents-in-the-backbone",
		Body: specBody, CreatedBy: "stig",
	})
	if err := replaceDocEdges(t, s, plan.ID); err != nil {
		t.Fatalf("ReplaceDocEdges: %v", err)
	}
	want := []model.DocEdge{
		// 999-nowhere.md names no document here and stays verbatim.
		{Type: "covers", ToExternal: "999-nowhere.md#sec-1"},
		{Type: "covers", ToDoc: spec.ID, ToAnchor: "sec-5"},
		{Type: "wasDerivedFrom", ToDoc: spec.ID},
	}
	if got := docEdges(t, s, plan.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
	after, err := s.GetDoc(t.Context(), plan.ID)
	if err != nil {
		t.Fatalf("GetDoc: %v", err)
	}
	if after.Version != 1 || after.Status != "accepted" || after.Body != planBody {
		t.Errorf("doc = {version:%d status:%s}, want the source untouched at version 1",
			after.Version, after.Status)
	}

	// An accepted spec keeps every published anchor and its version: the pass
	// reads the same body, it does not restate it.
	acceptedSpec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 26, Slug: "026-x",
		Body: specBody, CreatedBy: "stig", Status: "accepted",
	})
	sectionsBefore := docSections(t, s, acceptedSpec.ID)
	if err := replaceDocEdges(t, s, acceptedSpec.ID); err != nil {
		t.Fatalf("ReplaceDocEdges on an accepted spec: %v", err)
	}
	if got := docSections(t, s, acceptedSpec.ID); !slices.Equal(got, sectionsBefore) {
		t.Errorf("sections = %+v, want %+v", got, sectionsBefore)
	}

	if err := replaceDocEdges(t, s, 4711); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}
}

// TestDocCoverageLevels: a full, a partial with a resolvable
// fullCoverageWith, and a none entry each land with their authored level on
// the covers edge, and only the partial entry writes a
// doc_coverage_completed_with row (026 §2.1, §5).
func TestDocCoverageLevels(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	other := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "other-plan",
		Body: "---\nstatus: draft\n---\n\n# Other plan\n", CreatedBy: "stig",
	})

	body := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: full
  - spec: 025-documents-in-the-backbone.md#sec-2
    coverage: partial
    fullCoverageWith:
      - other-plan.md
  - spec: 025-documents-in-the-backbone.md#sec-2.1
    coverage: none
---

# Main plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})

	edges := docCoverageEdges(t, s, plan.ID)
	want := []docCoverageEdge{
		{toDoc: spec.ID, toAnchor: "sec-1", coverage: "full"},
		{toDoc: spec.ID, toAnchor: "sec-2", coverage: "partial"},
		{toDoc: spec.ID, toAnchor: "sec-2.1", coverage: "none"},
	}
	if len(edges) != len(want) {
		t.Fatalf("edges = %+v, want %+v", edges, want)
	}
	for i, e := range edges {
		if e.toDoc != want[i].toDoc || e.toAnchor != want[i].toAnchor || e.coverage != want[i].coverage {
			t.Errorf("edge %d = %+v, want %+v", i, e, want[i])
		}
	}

	if cw := docCompletedWith(t, s, edges[0].id); len(cw) != 0 {
		t.Errorf("full edge completedWith = %+v, want none", cw)
	}
	wantCW := []docCompletedWithRow{{position: 0, toDoc: other.ID}}
	if cw := docCompletedWith(t, s, edges[1].id); len(cw) != 1 || cw[0] != wantCW[0] {
		t.Errorf("partial edge completedWith = %+v, want %+v", cw, wantCW)
	}
	if cw := docCompletedWith(t, s, edges[2].id); len(cw) != 0 {
		t.Errorf("none edge completedWith = %+v, want none", cw)
	}
}

// TestDocCoverageFullCoverageWithUnresolved: a fullCoverageWith reference
// this project cannot resolve lands verbatim in to_external, to_doc NULL —
// unresolvable, it closes nothing (026 §2.1).
func TestDocCoverageFullCoverageWithUnresolved(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: partial
    fullCoverageWith:
      - nowhere-plan.md
---

# Plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})

	edges := docCoverageEdges(t, s, plan.ID)
	if len(edges) != 1 {
		t.Fatalf("edges = %+v, want 1", edges)
	}
	cw := docCompletedWith(t, s, edges[0].id)
	want := []docCompletedWithRow{{position: 0, toExternal: "nowhere-plan.md"}}
	if len(cw) != 1 || cw[0] != want[0] {
		t.Errorf("completedWith = %+v, want %+v", cw, want)
	}
}

// TestDocCoverageFullCoverageWithBlankEntryKeepsPositionsContiguous: a blank
// fullCoverageWith entry is dropped rather than stored, and the surviving
// rows' positions stay a contiguous 0-based rank rather than skipping the
// dropped entry's index.
func TestDocCoverageFullCoverageWithBlankEntryKeepsPositionsContiguous(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	other := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "other-plan", Body: planBody, CreatedBy: "stig"})

	body := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: partial
    fullCoverageWith:
      - ""
      - other-plan.md
---

# Plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})

	edges := docCoverageEdges(t, s, plan.ID)
	if len(edges) != 1 {
		t.Fatalf("edges = %+v, want 1", edges)
	}
	cw := docCompletedWith(t, s, edges[0].id)
	want := []docCompletedWithRow{{position: 0, toDoc: other.ID}}
	if len(cw) != 1 || cw[0] != want[0] {
		t.Errorf("completedWith = %+v, want %+v (blank entry dropped, position 0 not 1)", cw, want)
	}
}

// TestDocCoverageFullCoverageWithBesideFullWritesNoRows: fullCoverageWith is
// only meaningful on a partial entry (026 §5.1); beside full it is dropped
// rather than written.
func TestDocCoverageFullCoverageWithBesideFullWritesNoRows(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "other-plan",
		Body: "---\nstatus: draft\n---\n\n# Other plan\n", CreatedBy: "stig",
	})

	body := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: full
    fullCoverageWith:
      - other-plan.md
---

# Plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})

	edges := docCoverageEdges(t, s, plan.ID)
	if len(edges) != 1 || edges[0].coverage != "full" {
		t.Fatalf("edges = %+v, want one full edge", edges)
	}
	if cw := docCompletedWith(t, s, edges[0].id); len(cw) != 0 {
		t.Errorf("completedWith = %+v, want none", cw)
	}
}

// TestDocCoverageBareStringIsFull: a bare-string covers entry has no level
// to author, so it stores full — the decoder's default (026 §5.1).
func TestDocCoverageBareStringIsFull(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	body := "---\nstatus: draft\ncovers: 025-documents-in-the-backbone.md#sec-1\n---\n\n# Plan\n"
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})

	edges := docCoverageEdges(t, s, plan.ID)
	if len(edges) != 1 || edges[0].coverage != "full" {
		t.Fatalf("edges = %+v, want one full edge", edges)
	}
}

// TestDocCoverageRewriteReplacesCompletedWith: editing the body rebuilds
// doc_coverage_completed_with from the new source with no orphaned or
// duplicated rows, the same as it rebuilds doc_edges.
func TestDocCoverageRewriteReplacesCompletedWith(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	other := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "other-plan",
		Body: "---\nstatus: draft\n---\n\n# Other plan\n", CreatedBy: "stig",
	})
	third := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "third-plan",
		Body: "---\nstatus: draft\n---\n\n# Third plan\n", CreatedBy: "stig",
	})

	firstBody := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-2
    coverage: partial
    fullCoverageWith:
      - other-plan.md
---

# Main plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: firstBody, CreatedBy: "stig",
	})
	firstEdges := docCoverageEdges(t, s, plan.ID)
	if len(firstEdges) != 1 {
		t.Fatalf("edges = %+v, want 1", firstEdges)
	}
	firstEdgeID := firstEdges[0].id

	secondBody := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-2
    coverage: partial
    fullCoverageWith:
      - third-plan.md
      - other-plan.md
---

# Main plan
`
	if _, err := updateDocBody(t, s, plan.ID, secondBody); err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}

	// The old edge row (and its FK-cascaded completedWith rows) is gone.
	if cw := docCompletedWith(t, s, firstEdgeID); len(cw) != 0 {
		t.Errorf("stale completedWith rows for the deleted edge = %+v, want none", cw)
	}

	edges := docCoverageEdges(t, s, plan.ID)
	if len(edges) != 1 {
		t.Fatalf("edges after rewrite = %+v, want 1", edges)
	}
	got := docCompletedWith(t, s, edges[0].id)
	want := []docCompletedWithRow{
		{position: 0, toDoc: third.ID},
		{position: 1, toDoc: other.ID},
	}
	if len(got) != len(want) {
		t.Fatalf("completedWith = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("completedWith[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDocCoverageSameSectionTwiceRejectsDifferentLevels: two entries naming
// the same spec section at different levels contradict each other (026
// §2.1), so the write is refused rather than silently picking one.
func TestDocCoverageSameSectionTwiceRejectsDifferentLevels(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: full
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: partial
---

# Plan
`
	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocCoverageSameSectionTwiceSameLevelDeduped: two entries naming the
// same spec section at the same level are one edge, same as any other
// repeated resolved target.
func TestDocCoverageSameSectionTwiceSameLevelDeduped(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: full
  - 025-documents-in-the-backbone.md#sec-1
---

# Plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	if edges := docCoverageEdges(t, s, plan.ID); len(edges) != 1 || edges[0].coverage != "full" {
		t.Fatalf("edges = %+v, want one full edge", edges)
	}
}

// TestDocCoverageSameSectionTwicePartialRejectsDifferentClosures: two
// `partial` entries for the same section with different fullCoverageWith
// closures are the same class of contradiction as two different levels (026
// §2.1), so the write is refused rather than silently keeping one.
func TestDocCoverageSameSectionTwicePartialRejectsDifferentClosures(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "sibling-a", Body: planBody, CreatedBy: "stig"})
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "sibling-b", Body: planBody, CreatedBy: "stig"})

	body := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: partial
    fullCoverageWith:
      - sibling-a
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: partial
    fullCoverageWith:
      - sibling-b
---

# Plan
`
	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocCoverageSameSectionTwicePartialSameClosureDeduped: two `partial`
// entries for the same section naming the same fullCoverageWith target under
// different spellings are one edge — the dedupe key is the resolved closure,
// not the raw reference, matching why the row itself dedupes on the resolved
// target (026 §2.1).
func TestDocCoverageSameSectionTwicePartialSameClosureDeduped(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "sibling-plan", Body: planBody, CreatedBy: "stig"})

	body := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: partial
    fullCoverageWith:
      - sibling-plan
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: partial
    fullCoverageWith:
      - sibling-plan.md
---

# Plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	edges := docCoverageEdges(t, s, plan.ID)
	if len(edges) != 1 || edges[0].coverage != "partial" {
		t.Fatalf("edges = %+v, want one partial edge", edges)
	}
	if cw := docCompletedWith(t, s, edges[0].id); len(cw) != 1 {
		t.Fatalf("completedWith = %+v, want one row", cw)
	}
}

// TestDocCoverageUnknownLevelRejected: a coverage level outside
// full/partial/none must never reach the CHECK constraint as a raw Postgres
// error — it is ErrInvalidInput at the write.
func TestDocCoverageUnknownLevelRejected(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: mostly
---

# Plan
`
	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// --- defers (026 §5.3) ------------------------------------------------

// TestDocDefersCreatesEdgeAndOwner: an accepted plan's defers entry becomes
// one doc_edges row of type defers with to_doc/to_anchor set and coverage
// NULL, plus one doc_coverage_completed_with row resolving to the owner (026
// §5.3).
func TestDocDefersCreatesEdgeAndOwner(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	owner := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6,
		Slug: "006-knowledge-graph", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
defers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    to: 006-knowledge-graph.md
---

# Plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})

	got := docEdges(t, s, plan.ID)
	want := []model.DocEdge{{Type: "defers", ToDoc: spec.ID, ToAnchor: "sec-1"}}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want[0]) {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}

	edges := docCoverageEdges(t, s, plan.ID) // covers only, empty for a defers-only plan
	if len(edges) != 0 {
		t.Fatalf("covers edges = %+v, want none", edges)
	}

	var edgeID int64
	var coverage sql.NullString
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT id, coverage FROM doc_edges WHERE from_doc = $1 AND type = 'defers'`, plan.ID,
	).Scan(&edgeID, &coverage); err != nil {
		t.Fatalf("read defers edge: %v", err)
	}
	if coverage.Valid {
		t.Errorf("defers edge coverage = %q, want NULL", coverage.String)
	}
	cw := docCompletedWith(t, s, edgeID)
	want2 := []docCompletedWithRow{{position: 0, toDoc: owner.ID}}
	if len(cw) != 1 || cw[0] != want2[0] {
		t.Errorf("completedWith = %+v, want %+v", cw, want2)
	}
}

// TestDocDefersOnSpecRejected: defers is plan-only — a spec defers nothing,
// it is what work is deferred *from* (026 §5.3).
func TestDocDefersOnSpecRejected(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6,
		Slug: "006-knowledge-graph", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
defers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    to: 006-knowledge-graph.md
---

# A spec

## 1. Scope {#sec-1}

Scope body.
`
	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: body, CreatedBy: "stig",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocDefersMissingFragmentRejected: a defers entry whose spec reference
// carries no #sec-N fragment is refused, not tolerated-and-ignored the way a
// whole-document covers claim is (026 §5.3).
func TestDocDefersMissingFragmentRejected(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6,
		Slug: "006-knowledge-graph", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
defers:
  - spec: 025-documents-in-the-backbone.md
    to: 006-knowledge-graph.md
---

# Plan
`
	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocDefersEmptyOwnerRejected: a deferral without an owner is just an
// uncovered section, which needs no syntax — omitting `to` is refused (026
// §5.3).
func TestDocDefersEmptyOwnerRejected(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
defers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    to: ""
---

# Plan
`
	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocDefersOwnerWithFragmentRejected: the owner is a document, never a
// section — a `to` carrying a #sec-N fragment is refused, matching
// secmeta.py's check rather than silently stripping the fragment (026 §5.3).
func TestDocDefersOwnerWithFragmentRejected(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
defers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    to: 025-documents-in-the-backbone.md#sec-2
---

# Plan
`
	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocDefersToItselfRejected: a plan deferring a section to itself has
// confused deferral with coverage; refused (026 §5.3).
func TestDocDefersToItselfRejected(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
defers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    to: main-plan
---

# Plan
`
	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocDefersSameSectionTwoOwnersRejected: the same section deferred to two
// different owners is a contradiction the frontmatter cannot mean, refused
// the way conflicting covers levels are (026 §5.3, §5.1).
func TestDocDefersSameSectionTwoOwnersRejected(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6,
		Slug: "006-knowledge-graph", Body: specBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 4,
		Slug: "004-execution-backbone", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
defers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    to: 006-knowledge-graph.md
  - spec: 025-documents-in-the-backbone.md#sec-1
    to: 004-execution-backbone.md
---

# Plan
`
	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocDefersIdenticalEntryTwiceDeduped: the same entry twice is one edge,
// not an error (026 §5.3).
func TestDocDefersIdenticalEntryTwiceDeduped(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6,
		Slug: "006-knowledge-graph", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
defers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    to: 006-knowledge-graph.md
  - spec: 025-documents-in-the-backbone.md#sec-1
    to: 006-knowledge-graph.md
---

# Plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	got := docEdges(t, s, plan.ID)
	if len(got) != 1 {
		t.Fatalf("edges = %+v, want one edge", got)
	}
}

// TestDocDefersUnresolvableSpecLandsInExternal: an unresolvable `spec`
// reference lands verbatim in to_external, same as a covers typo — it reads
// as an unplanned section rather than an error (026 §5.3).
func TestDocDefersUnresolvableSpecLandsInExternal(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6,
		Slug: "006-knowledge-graph", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
defers:
  - spec: 999-nowhere.md#sec-1
    to: 006-knowledge-graph.md
---

# Plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	got := docEdges(t, s, plan.ID)
	want := []model.DocEdge{{Type: "defers", ToExternal: "999-nowhere.md#sec-1"}}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want[0]) {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
}

// TestDocSchemaDefersEdgeSucceeds: migration 0045 admits 'defers' to
// doc_edges' type CHECK.
func TestDocSchemaDefersEdgeSucceeds(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	seedDocsProject(t, s)

	planID, err := insertDoc(t, s, "plan", 1, "plan-a")
	if err != nil {
		t.Fatalf("insert plan doc: %v", err)
	}
	specID, err := insertDoc(t, s, "spec", 25, "documents-in-the-backbone")
	if err != nil {
		t.Fatalf("insert spec doc: %v", err)
	}

	ctx := context.Background()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO doc_edges (from_doc, type, to_doc, to_anchor, declared_by)
		 VALUES ($1, 'defers', $2, 'sec-1', $1)`,
		planID, specID)
	if err != nil {
		t.Fatalf("insert defers edge: %v", err)
	}
}

// TestDocSchemaBogusEdgeTypeViolatesCheck: doc_edges_type_check still refuses
// a type outside the admitted set (migration 0045 only adds 'defers' to it).
func TestDocSchemaBogusEdgeTypeViolatesCheck(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	seedDocsProject(t, s)

	planID, err := insertDoc(t, s, "plan", 1, "plan-a")
	if err != nil {
		t.Fatalf("insert plan doc: %v", err)
	}
	specID, err := insertDoc(t, s, "spec", 25, "documents-in-the-backbone")
	if err != nil {
		t.Fatalf("insert spec doc: %v", err)
	}

	ctx := context.Background()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO doc_edges (from_doc, type, to_doc, to_anchor, declared_by)
		 VALUES ($1, 'bogus', $2, 'sec-1', $1)`,
		planID, specID)
	if err == nil {
		t.Fatal("expected CHECK violation, got nil error")
	}
	if !isCheckViolationOn(err, "doc_edges_type_check") {
		t.Fatalf("expected doc_edges_type_check CHECK violation, got: %v", err)
	}
}

// TestDocResolveRefShorthand covers 025 §14.3's <KEY>-<TYPE>-<n> form against
// the referring document's own corpus: the key must be a real project's, and
// the type token is verified against the target's kind rather than trusted.
func TestDocResolveRefShorthand(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
requires:
  - P1-SPEC-25#sec-2
  - ZZ-SPEC-25
  - P1-ADR-25
---

# Referring plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "referring", Body: body, CreatedBy: "stig",
	})

	got := docEdges(t, s, plan.ID)
	// Unresolved references (to_doc NULL, coalesced to 0) sort ahead of the
	// resolved one. This project's key is P1, so ZZ- names a corpus we cannot
	// reach; P1-ADR-25 names a kind this project has no 25 of.
	want := []model.DocEdge{
		{Type: "requires", ToExternal: "P1-ADR-25"},
		{Type: "requires", ToExternal: "ZZ-SPEC-25"},
		{Type: "requires", ToDoc: spec.ID, ToAnchor: "sec-2"},
	}
	if len(got) != len(want) {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("edge %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDocResolveRefShorthandCrossesProjects: 025 §14.3's "distance decides
// which form is canonical". The shorthand is the form for a reference across
// corpora, so it resolves on the project key alone; a filename and a bare
// number carry no corpus and stay same-project, landing in to_external when
// only another project holds the target.
func TestDocResolveRefShorthandCrossesProjects(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO projects (id, name, key) VALUES ('cms','CMS','CMS')`); err != nil {
		t.Fatal(err)
	}
	// The target lives in cms only; p1 holds no spec 4 and no such slug.
	target := mustCreateDoc(t, s, DocInput{
		Project: "cms", Kind: "spec", Number: 4, Slug: "004-content-model",
		Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
requires:
  - CMS-SPEC-4#sec-3
  - 004-content-model.md
  - "004"
---

# Referring plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "referring", Body: body, CreatedBy: "stig",
	})

	// Unresolved edges (to_doc NULL, coalesced to 0) sort ahead of the
	// resolved one.
	want := []model.DocEdge{
		{Type: "requires", ToExternal: "004"},
		{Type: "requires", ToExternal: "004-content-model.md"},
		{Type: "requires", ToDoc: target.ID, ToAnchor: "sec-3"},
	}
	got := docEdges(t, s, plan.ID)
	if len(got) != len(want) {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("edge %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestProjectKeySpecAndADRReserved: SPEC and ADR are the <TYPE> token of the
// document shorthand, which resolves on the project key alone (025 §14.3), so
// the projects_key_format CHECK rejects them as keys.
func TestProjectKeySpecAndADRReserved(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	for _, key := range []string{"SPEC", "ADR"} {
		_, err := s.db.ExecContext(t.Context(),
			`INSERT INTO projects (id, name, key) VALUES ($1, $1, $2)`,
			strings.ToLower(key), key)
		if err == nil {
			t.Errorf("project key %q accepted, want rejected by projects_key_format", key)
			continue
		}
		if !strings.Contains(err.Error(), "projects_key_format") {
			t.Errorf("project key %q: err = %v, want a projects_key_format violation", key, err)
		}
	}
	// A key that merely contains them is still fine.
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO projects (id, name, key) VALUES ('specs','Specs','SPECS')`); err != nil {
		t.Errorf("project key SPECS rejected: %v", err)
	}
}

// TestDocResolveRefBareNumberAmbiguous: a project may hold a spec 25 and an
// ADR 25. A bare number cannot say which, so it resolves to neither.
func TestDocResolveRefBareNumberAmbiguous(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-spec", Body: specBody, CreatedBy: "stig",
	})

	// While 25 names only the spec, the bare number resolves.
	unambiguous := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-a", CreatedBy: "stig",
		Body: "---\nstatus: draft\nrequires: \"025\"\n---\n\n# A\n",
	})
	if edges := docEdges(t, s, unambiguous.ID); len(edges) != 1 || edges[0].ToDoc != spec.ID {
		t.Fatalf("edges = %+v, want one resolving to the spec", edges)
	}

	// Add an ADR 25 and the same reference becomes ambiguous.
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "adr", Number: 25, Slug: "025-adr", Body: specBody, CreatedBy: "stig",
	})
	ambiguous := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-b", CreatedBy: "stig",
		Body: "---\nstatus: draft\nrequires: \"025\"\n---\n\n# B\n",
	})
	want := model.DocEdge{Type: "requires", ToExternal: "025"}
	if edges := docEdges(t, s, ambiguous.ID); len(edges) != 1 || !reflect.DeepEqual(edges[0], want) {
		t.Fatalf("edges = %+v, want %+v", edges, want)
	}
}

// TestDocResolveRefNumberPrefixIsNotANumber: a number-prefixed filename that
// matches no slug is a miss, not spec 025. Resolving on the shared prefix
// would turn "025-…-2.md" into an edge to spec 025 — a wrong edge is worse
// than an unresolved one.
func TestDocResolveRefNumberPrefixIsNotANumber(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	body := "---\nstatus: draft\n" +
		"requires: 025-documents-in-the-backbone-2.md\n---\n\n# Plan\n"
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-a", Body: body, CreatedBy: "stig",
	})

	want := model.DocEdge{Type: "requires", ToExternal: "025-documents-in-the-backbone-2.md"}
	if edges := docEdges(t, s, plan.ID); len(edges) != 1 || !reflect.DeepEqual(edges[0], want) {
		t.Fatalf("edges = %+v, want %+v", edges, want)
	}
}

// --- editorial lifecycle (025 §6, §7) ---------------------------------------

// TestDocListEdgesBothDirections: the same row is read forward out of the
// document that declared it and backward into the document it names, where it
// carries its inverse spelling and points back at the other end (025 §14).
// Every resolved far end also carries the other document's project, slug, kind
// and number, so a reader can name it; an unresolved reference carries none.
func TestDocListEdgesBothDirections(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan",
		Slug: "025-documents-in-the-backbone-2", Body: planBody, CreatedBy: "stig",
	})

	out, in, err := s.ListDocEdges(t.Context(), plan.ID)
	if err != nil {
		t.Fatalf("ListDocEdges(plan): %v", err)
	}
	// Ordered by the stored columns with NULL coalesced away, so an
	// unresolvable reference (to_doc NULL -> 0) sorts ahead of a resolved one.
	specFar := func(e model.DocEdge) model.DocEdge {
		e.ToDoc, e.ToProject, e.ToSlug, e.ToKind, e.ToNumber = spec.ID, "p1", spec.Slug, "spec", 25
		return e
	}
	wantOut := []model.DocEdge{
		{Type: "covers", ToExternal: "999-nowhere.md#sec-1"},
		specFar(model.DocEdge{Type: "covers", ToAnchor: "sec-5"}),
		specFar(model.DocEdge{Type: "wasDerivedFrom"}),
	}
	if len(out) != len(wantOut) {
		t.Fatalf("plan edges out = %+v, want %+v", out, wantOut)
	}
	for i := range wantOut {
		if !reflect.DeepEqual(out[i], wantOut[i]) {
			t.Errorf("plan edge out %d = %+v, want %+v", i, out[i], wantOut[i])
		}
	}
	if len(in) != 0 {
		t.Errorf("plan edges in = %+v, want none", in)
	}

	out, in, err = s.ListDocEdges(t.Context(), spec.ID)
	if err != nil {
		t.Fatalf("ListDocEdges(spec): %v", err)
	}
	if len(out) != 1 || !reflect.DeepEqual(out[0], model.DocEdge{Type: "requires", ToExternal: "004-execution-backbone.md#sec-6"}) {
		t.Fatalf("spec edges out = %+v, want one external requires", out)
	}
	// The covers edge lands on the spec's #sec-5, so from the spec's end that
	// is the near anchor and the plan is the far end.
	planFar := func(e model.DocEdge) model.DocEdge {
		// Since 029 §4 a plan carries a number like every other kind, so its
		// far end names one too.
		e.ToDoc, e.ToProject, e.ToSlug, e.ToKind = plan.ID, "p1", plan.Slug, "plan"
		e.ToNumber = plan.Number
		return e
	}
	wantIn := []model.DocEdge{
		planFar(model.DocEdge{Type: "isCoveredBy", FromAnchor: "sec-5"}),
		planFar(model.DocEdge{Type: "hadDerivation"}),
	}
	if len(in) != len(wantIn) {
		t.Fatalf("spec edges in = %+v, want %+v", in, wantIn)
	}
	for i := range wantIn {
		if !reflect.DeepEqual(in[i], wantIn[i]) {
			t.Errorf("spec edge in %d = %+v, want %+v", i, in[i], wantIn[i])
		}
	}
}

// TestDocListEdgesResolvesFarProject: an edge can leave its project — the
// 025 §14.3 shorthand resolves on a project *key*, not within the declaring
// document's project — so the resolved far end names the project it landed in
// and not the one it left. Both directions, since a client that addresses a
// document by project and slug (the Obsidian mirror's doc wikilinks, WL-284)
// would otherwise silently assume the near end's project for either.
func TestDocListEdgesResolvesFarProject(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO projects (id, name, key) VALUES ('p2','P2','P2')`); err != nil {
		t.Fatal(err)
	}

	far := mustCreateDoc(t, s, DocInput{
		Project: "p2", Kind: "spec", Number: 7, Slug: "007-far-spec", Body: specBody, CreatedBy: "stig",
	})
	// "P2-SPEC-7" is the shorthand for spec 7 of project P2, stated from a
	// document in p1.
	near := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-across", CreatedBy: "stig",
		Body: "---\nstatus: draft\nwasDerivedFrom: P2-SPEC-7\n---\n\n# Plan across\n",
	})

	out, _, err := s.ListDocEdges(t.Context(), near.ID)
	if err != nil {
		t.Fatalf("ListDocEdges(near): %v", err)
	}
	want := []model.DocEdge{{
		Type: "wasDerivedFrom", ToDoc: far.ID,
		ToProject: "p2", ToSlug: "007-far-spec", ToKind: "spec", ToNumber: 7,
	}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("edges out of the near plan = %+v, want %+v", out, want)
	}

	_, in, err := s.ListDocEdges(t.Context(), far.ID)
	if err != nil {
		t.Fatalf("ListDocEdges(far): %v", err)
	}
	wantIn := []model.DocEdge{{
		Type: "hadDerivation", ToDoc: near.ID, ToNumber: near.Number,
		ToProject: "p1", ToSlug: "plan-across", ToKind: "plan",
	}}
	if !reflect.DeepEqual(in, wantIn) {
		t.Fatalf("edges into the far spec = %+v, want %+v", in, wantIn)
	}
}

// TestDocListEdgesIncludesCompletedWith: doc_coverage_completed_with backs a
// partial covers entry's fullCoverageWith closure and a defers entry's owner
// alike (026 §5, §5.3), but ListDocEdges did not join it in — a document's
// own edge listing understated what its frontmatter asserted (WL-291), even
// though NeedsPlanning already resolved the owner from the same table.
// Checked in both directions: the plan's own covers/defers row, and the
// spec's inbound isCoveredBy/isDeferredBy reading of that same row.
func TestDocListEdgesIncludesCompletedWith(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustAcceptedSpec(t, s, "025-x")
	owner := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	closer := levelledPlan(t, s, "closer-plan", true, coverageRef{ref: "025-x#sec-1", level: "full"})
	partial := levelledPlan(t, s, "partial-plan", true,
		coverageRef{ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{"closer-plan.md"}})
	deferrer := deferringPlan(t, s, "deferring-plan", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})

	out, _, err := s.ListDocEdges(t.Context(), partial.ID)
	if err != nil {
		t.Fatalf("ListDocEdges(partial): %v", err)
	}
	if len(out) != 1 || out[0].Type != "covers" {
		t.Fatalf("partial plan edges = %+v, want one covers edge", out)
	}
	if want := []string{closer.Slug}; !slices.Equal(out[0].CompletedWith, want) {
		t.Errorf("covers edge CompletedWith = %v, want %v", out[0].CompletedWith, want)
	}

	out, _, err = s.ListDocEdges(t.Context(), deferrer.ID)
	if err != nil {
		t.Fatalf("ListDocEdges(deferrer): %v", err)
	}
	if len(out) != 1 || out[0].Type != "defers" {
		t.Fatalf("deferring plan edges = %+v, want one defers edge", out)
	}
	if want := []string{owner.Slug}; !slices.Equal(out[0].CompletedWith, want) {
		t.Errorf("defers edge CompletedWith = %v, want %v", out[0].CompletedWith, want)
	}

	_, in, err := s.ListDocEdges(t.Context(), spec.ID)
	if err != nil {
		t.Fatalf("ListDocEdges(spec): %v", err)
	}
	var sawCovered, sawDeferred bool
	for _, e := range in {
		switch {
		case e.Type == "isCoveredBy" && e.ToDoc == partial.ID:
			sawCovered = true
			if want := []string{closer.Slug}; !slices.Equal(e.CompletedWith, want) {
				t.Errorf("isCoveredBy CompletedWith = %v, want %v", e.CompletedWith, want)
			}
		case e.Type == "isDeferredBy" && e.ToDoc == deferrer.ID:
			sawDeferred = true
			if want := []string{owner.Slug}; !slices.Equal(e.CompletedWith, want) {
				t.Errorf("isDeferredBy CompletedWith = %v, want %v", e.CompletedWith, want)
			}
		}
	}
	if !sawCovered {
		t.Errorf("no isCoveredBy edge from partial-plan in spec's inbound edges: %+v", in)
	}
	if !sawDeferred {
		t.Errorf("no isDeferredBy edge from deferring-plan in spec's inbound edges: %+v", in)
	}
}

// TestDocListEdgesInverseCoversEveryType: every type the doc_edges CHECK
// admits must have an inverse, or reading a document's inbound edges states
// the relation backwards. One edge of each type, read from the far end.
func TestDocListEdgesInverseCoversEveryType(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	from := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-from", Body: specBody, CreatedBy: "stig",
	})
	to := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 26, Slug: "026-to", Body: specBody, CreatedBy: "stig",
	})

	types := []string{"covers", "implements", "amends", "replaces", "requires", "wasDerivedFrom", "blocks"}
	for _, typ := range types {
		// Only a covers edge carries a coverage level
		// (doc_edges_coverage_on_covers).
		var coverage sql.NullString
		if typ == "covers" {
			coverage = sql.NullString{String: "full", Valid: true}
		}
		if _, err := s.db.ExecContext(t.Context(),
			`INSERT INTO doc_edges (from_doc, type, to_doc, coverage, declared_by)
			 VALUES ($1, $2, $3, $4, $1)`,
			from.ID, typ, to.ID, coverage); err != nil {
			t.Fatalf("insert %s edge: %v", typ, err)
		}
	}

	_, in, err := s.ListDocEdges(t.Context(), to.ID)
	if err != nil {
		t.Fatalf("ListDocEdges: %v", err)
	}
	if len(in) != len(types) {
		t.Fatalf("inbound edges = %+v, want %d", in, len(types))
	}
	for _, e := range in {
		if e.ToDoc != from.ID {
			t.Errorf("inbound %s points at %d, want the other end %d", e.Type, e.ToDoc, from.ID)
		}
		if slices.Contains(types, e.Type) {
			t.Errorf("inbound edge kept its forward spelling %q", e.Type)
		}
	}
}

// TestDocBlockedByWritesTheSameRowAsBlocks: the whole point of the inverse
// spelling is authoring order. A numbered plan series is written forward, so
// part 3 must be able to say it follows part 2 without part 2 being edited —
// and the row it writes is byte-for-byte the one part 2's `blocks:` would
// have written (025 §5, WL-143).
func TestDocBlockedByWritesTheSameRowAsBlocks(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	// Forward: the earlier plan first, the later plan naming it.
	early := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-early", Body: planMintBody, CreatedBy: "stig",
	})
	late := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-late", CreatedBy: "stig",
		Body: "---\nstatus: draft\nblockedBy: plan-early\n---\n\n# Plan late\n",
	})

	if got := docEdges(t, s, late.ID); len(got) != 0 {
		t.Fatalf("edges of plan-late = %+v, want none: the row leaves the blocking plan", got)
	}
	got := docEdges(t, s, early.ID)
	want := []model.DocEdge{{Type: "blocks", ToDoc: late.ID}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("edges of plan-early = %+v, want %+v", got, want)
	}

	// Backward: the same ordering over a second pair, declared the old way.
	// The row is the same shape, which is the claim — `blockedBy` is a
	// spelling, not a second kind of edge.
	b := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-second-late", Body: planMintBody, CreatedBy: "stig",
	})
	a := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-second-early", CreatedBy: "stig",
		Body: "---\nstatus: draft\nblocks: plan-second-late\n---\n\n# Plan early\n",
	})
	if got, want := docEdges(t, s, a.ID), []model.DocEdge{{Type: "blocks", ToDoc: b.ID}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("edges of plan-second-early declared with blocks = %+v, want %+v", got, want)
	}
}

// TestDocBlockedByIsOwnedByItsAuthor: the row leaves the *other* plan, so the
// rewrite that clears it has to be scoped by who declared it (doc_edges
// .declared_by), not by where it points from. Dropping the key drops the row;
// rewriting the blocking plan does not.
func TestDocBlockedByIsOwnedByItsAuthor(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	early := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-early", Body: planMintBody, CreatedBy: "stig",
	})
	late := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-late", CreatedBy: "stig",
		Body: "---\nstatus: draft\nblockedBy: plan-early\n---\n\n# Plan late\n",
	})

	// The blocking plan's own rewrite leaves the row standing: it is not its
	// declaration to clear.
	if _, err := updateDocBody(t, s, early.ID, planMintBody+"\nMore prose.\n"); err != nil {
		t.Fatalf("rewrite plan-early: %v", err)
	}
	want := []model.DocEdge{{Type: "blocks", ToDoc: late.ID}}
	if got := docEdges(t, s, early.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("edges of plan-early after its own rewrite = %+v, want %+v", got, want)
	}

	// The declaring plan dropping the key clears it.
	if _, err := updateDocBody(t, s, late.ID, "---\nstatus: draft\n---\n\n# Plan late\n"); err != nil {
		t.Fatalf("rewrite plan-late without blockedBy: %v", err)
	}
	if got := docEdges(t, s, early.ID); len(got) != 0 {
		t.Fatalf("edges of plan-early after plan-late dropped blockedBy = %+v, want none", got)
	}
}

// TestDocBlocksDeclaredFromBothEndsIsOneRow: both plans spelling the same
// ordering is the same fact twice, not a contradiction. It stays one row —
// the unique index is the arbiter — and the later writer owns it.
func TestDocBlocksDeclaredFromBothEndsIsOneRow(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	early := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-early", Body: planMintBody, CreatedBy: "stig",
	})
	late := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-late", CreatedBy: "stig",
		Body: "---\nstatus: draft\nblockedBy: plan-early\n---\n\n# Plan late\n",
	})
	if _, err := updateDocBody(t, s, early.ID,
		"---\nstatus: draft\nblocks: plan-late\n---\n\n# Plan early\n"); err != nil {
		t.Fatalf("add blocks to plan-early: %v", err)
	}

	want := []model.DocEdge{{Type: "blocks", ToDoc: late.ID}}
	if got := docEdges(t, s, early.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("edges of plan-early = %+v, want exactly %+v", got, want)
	}
}

// TestDocEdgesRejectBadBlockedByEnds: `blockedBy` is the same edge, so it is
// held to the same guards — both ends plans, the reference resolvable, no
// self-block, no cycle. Reading them off the row rather than off the author is
// what keeps the two spellings from disagreeing about what is legal.
func TestDocEdgesRejectBadBlockedByEnds(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "a-real-plan", Body: planMintBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-head", CreatedBy: "stig",
		Body: "---\nstatus: draft\nblocks: a-real-plan\n---\n\n# Plan\n",
	})

	cases := []struct {
		name string
		want string // substring, so the right guard is what refused
		in   DocInput
	}{
		{
			// The declaring plan is the *to* end here, so the spec lands on
			// the from end — the mirror of "spec blocks a plan".
			name: "plan blockedBy a spec",
			want: "the from end",
			in: DocInput{
				Project: "p1", Kind: "plan", Slug: "blocked-by-a-spec", CreatedBy: "stig",
				Body: "---\nstatus: draft\nblockedBy: 025-documents-in-the-backbone\n---\n\n# Plan\n",
			},
		},
		{
			name: "spec blockedBy a plan",
			want: "the to end",
			in: DocInput{
				Project: "p1", Kind: "spec", Number: 26, Slug: "026-blocked-spec", CreatedBy: "stig",
				Body: "---\nstatus: draft\nblockedBy: a-real-plan\n---\n\n# Spec\n\n## 1. One {#sec-1}\n\nx\n",
			},
		},
		{
			name: "plan blockedBy an unresolvable reference",
			want: "no plan in this project resolves to",
			in: DocInput{
				Project: "p1", Kind: "plan", Slug: "blocked-by-nowhere", CreatedBy: "stig",
				Body: "---\nstatus: draft\nblockedBy: 999-nowhere.md\n---\n\n# Plan\n",
			},
		},
		{
			name: "plan blockedBy itself",
			want: "cannot block itself",
			in: DocInput{
				Project: "p1", Kind: "plan", Slug: "self-blocked", CreatedBy: "stig",
				Body: "---\nstatus: draft\nblockedBy: self-blocked\n---\n\n# Plan\n",
			},
		},
		{
			// `blocks` is walked before `blockedBy`, so the first key has
			// already stored this-plan → plan-head when the second proposes
			// plan-head → this-plan: a two-plan cycle closed from the far end.
			name: "plan blockedBy a plan it already blocks",
			want: "plan-head blocks cycle-both-ways blocks plan-head",
			in: DocInput{
				Project: "p1", Kind: "plan", Slug: "cycle-both-ways", CreatedBy: "stig",
				Body: "---\nstatus: draft\nblocks: plan-head\nblockedBy: plan-head\n---\n\n# Plan\n",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := createDoc(t, s, tc.in)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("createDoc = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("createDoc = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestDocEdgesRejectBlocksBetweenNonPlans: `blocks` orders whole plan
// documents (025 §5). An end that is not a plan, or a reference this project
// cannot resolve to one, is ErrInvalidInput rather than a dead edge.
func TestDocEdgesRejectBlocksBetweenNonPlans(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "a-real-plan", Body: planMintBody, CreatedBy: "stig",
	})

	cases := []struct {
		name string
		want string // substring, so the right guard is what refused
		in   DocInput
	}{
		{
			name: "plan blocks a spec",
			want: "the to end",
			in: DocInput{
				Project: "p1", Kind: "plan", Slug: "blocks-a-spec", CreatedBy: "stig",
				Body: "---\nstatus: draft\nblocks: 025-documents-in-the-backbone\n---\n\n# Plan\n",
			},
		},
		{
			name: "spec blocks a plan",
			want: "the from end",
			in: DocInput{
				Project: "p1", Kind: "spec", Number: 26, Slug: "026-blocking-spec", CreatedBy: "stig",
				Body: "---\nstatus: draft\nblocks: a-real-plan\n---\n\n# Spec\n\n## 1. One {#sec-1}\n\nx\n",
			},
		},
		{
			name: "plan blocks an unresolvable reference",
			want: "no plan in this project resolves to",
			in: DocInput{
				Project: "p1", Kind: "plan", Slug: "blocks-nowhere", CreatedBy: "stig",
				Body: "---\nstatus: draft\nblocks: 999-nowhere.md\n---\n\n# Plan\n",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := createDoc(t, s, tc.in)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("createDoc = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("createDoc = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestDocEdgesRejectSelfBlockingPlan: a plan whose `blocks` names its own slug
// resolves to itself and would wedge its own task set forever — every task of
// the plan would block itself, and while the plan is draft the unminted-set
// arm blocks too. It is refused at write time (025 §5).
func TestDocEdgesRejectSelfBlockingPlan(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "self-blocker", CreatedBy: "stig",
		Body: "---\nstatus: draft\nblocks: self-blocker\n---\n\n# Plan\n",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("createDoc = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "cannot block itself") {
		t.Fatalf("createDoc = %v, want it to say a plan cannot block itself", err)
	}
}

// TestDocEdgesRejectBlocksCycleBetweenPlans: a cycle through two or more plans
// wedges every plan in it — each plan's tasks are held by the next plan's open
// set, so no set can ever close and Claim answers ErrBlocked forever. Plans
// stay mutable at any status, so it is the write that closes the cycle that
// has to refuse it, and the refusal names the cycle (WL-144).
func TestDocEdgesRejectBlocksCycleBetweenPlans(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	// a blocks b blocks c, written back to front so every reference resolves.
	c := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-c", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n\n# Plan\n",
	})
	b := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-b", CreatedBy: "stig",
		Body: blockingPlanBody("plan-c"),
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-a", CreatedBy: "stig",
		Body: blockingPlanBody("plan-b"),
	})

	// Two hops back to plan-a: the whole cycle is named, in order.
	_, err := updateDocBody(t, s, c.ID, blockingPlanBody("plan-a"))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("plan-c blocks plan-a = %v, want ErrInvalidInput", err)
	}
	if want := "plan-c blocks plan-a blocks plan-b blocks plan-c"; !strings.Contains(err.Error(), want) {
		t.Fatalf("plan-c blocks plan-a = %v, want it to name the cycle %q", err, want)
	}
	// The refused write left plan-c's edges alone, cycle or not.
	if edges := docEdges(t, s, c.ID); len(edges) != 0 {
		t.Fatalf("edges of plan-c = %+v, want none", edges)
	}

	// One hop back: the two-plan cycle the self-block guard never saw.
	_, err = updateDocBody(t, s, b.ID, blockingPlanBody("plan-a"))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("plan-b blocks plan-a = %v, want ErrInvalidInput", err)
	}
	if want := "plan-b blocks plan-a blocks plan-b"; !strings.Contains(err.Error(), want) {
		t.Fatalf("plan-b blocks plan-a = %v, want it to name the cycle %q", err, want)
	}
}

// TestDocEdgesAllowConvergingBlocks: the cycle guard refuses cycles, not
// re-convergence. A plan reachable by two distinct paths is an ordinary DAG
// and every task in it can still close.
func TestDocEdgesAllowConvergingBlocks(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	last := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-last", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n\n# Plan\n",
	})
	mid := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-mid", CreatedBy: "stig",
		Body: blockingPlanBody("plan-last"),
	})
	first := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-first", CreatedBy: "stig",
		Body: "---\nstatus: draft\nblocks:\n  - plan-mid\n  - plan-last\n---\n\n# Plan\n",
	})

	got := docEdges(t, s, first.ID)
	want := []model.DocEdge{
		{Type: "blocks", ToDoc: mid.ID},
		{Type: "blocks", ToDoc: last.ID},
	}
	slices.SortFunc(want, func(x, y model.DocEdge) int { return cmp.Compare(x.ToDoc, y.ToDoc) })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("edges of plan-first = %+v, want %+v", got, want)
	}
}

// --- NeedsPlanning / NeedsExecution (026 §2) -----------------------------

// TestReplaceDocEdgesSupersedesReplacedTarget: a document-level `replaces`
// edge can newly resolve through the repair path too, not only through
// CreateDoc's accepted-at-create path and repointExternalEdges (WL-133) —
// ReplaceDocEdges must run the same cascade (WL-278).
//
// The corpus shape here is one repointExternalEdges cannot reach on its own:
// the replacer is tombstoned when its target arrives, so the sweep — scoped
// to live referring documents — skips its edge. Restoring the replacer does
// not re-sweep it, so the edge is left exactly as WL-133 describes edges
// going stale "some other way": ReplaceDocEdges is the only pass left that
// re-reads its frontmatter, and it owes the cascade the other two paths owe.
func TestReplaceDocEdgesSupersedesReplacedTarget(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	successor := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Status: "accepted", Body: replacerBody("New", "006-old.md"),
	})
	if err := deleteDoc(t, s, successor.ID, "stig", "temporarily out of the corpus"); err != nil {
		t.Fatalf("DeleteDoc(025-new): %v", err)
	}

	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})

	if err := undeleteDoc(t, s, successor.ID); err != nil {
		t.Fatalf("UndeleteDoc(025-new): %v", err)
	}
	before := docEdges(t, s, successor.ID)
	want := []model.DocEdge{{Type: "replaces", ToExternal: "006-old.md"}}
	if !reflect.DeepEqual(before, want) {
		t.Fatalf("edges before repair = %+v, want %+v", before, want)
	}
	if got := docStatus(t, s, old.ID); got != "accepted" {
		t.Fatalf("006-old status before repair = %q, want accepted", got)
	}

	if err := replaceDocEdges(t, s, successor.ID); err != nil {
		t.Fatalf("ReplaceDocEdges(025-new): %v", err)
	}
	after := docEdges(t, s, successor.ID)
	wantAfter := []model.DocEdge{{Type: "replaces", ToDoc: old.ID}}
	if !reflect.DeepEqual(after, wantAfter) {
		t.Fatalf("edges after repair = %+v, want %+v", after, wantAfter)
	}
	if got := docStatus(t, s, old.ID); got != "superseded" {
		t.Fatalf("006-old status after repair = %q, want superseded", got)
	}
}

// TestFrontmatterEdgesBlockedByBecomesInverseBlocks: the spelling is resolved
// where the frontmatter is read, not where the row is written — `blockedBy`
// leaves frontmatterEdges as a `blocks` edge marked inverse, so every guard
// and every dedupe downstream sees one relation with one type and only the
// row's two ends move (025 §5, WL-143). No database: this is the translation
// itself.
func TestFrontmatterEdgesBlockedByBecomesInverseBlocks(t *testing.T) {
	t.Parallel()
	doc, err := designdoc.Parse([]byte(
		"---\nstatus: draft\nblocks: plan-three\nblockedBy: plan-one\n---\n\n# Plan two\n"))
	if err != nil {
		t.Fatalf("parse plan: %v", err)
	}

	got := frontmatterEdges(doc.Frontmatter)
	want := []docEdgeRef{
		{typ: "blocks", ref: "plan-three"},
		{typ: "blocks", ref: "plan-one", inverse: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frontmatterEdges = %+v, want %+v", got, want)
	}
}
