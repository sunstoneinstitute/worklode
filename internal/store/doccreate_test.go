package store

import (
	"cmp"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestDocCreateSpec: a spec body lands as a draft row whose title comes from
// the H1, whose issued comes from the frontmatter, and whose anchored
// sections mirror the source in document order.
func TestDocCreateSpec(t *testing.T) {
	s := openDocStore(t)

	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	if doc.Status != "draft" {
		t.Errorf("status = %q, want draft", doc.Status)
	}
	if doc.Version != 1 {
		t.Errorf("version = %d, want 1", doc.Version)
	}
	if doc.Title != "Documents in the backbone" {
		t.Errorf("title = %q, want the H1", doc.Title)
	}
	if doc.Issued != "2026-08-01" {
		t.Errorf("issued = %q, want 2026-08-01", doc.Issued)
	}
	if doc.Number != 25 {
		t.Errorf("number = %d, want 25", doc.Number)
	}

	secs := docSections(t, s, doc.ID)
	want := []model.DocSection{
		{Anchor: "sec-1", Number: "1", Heading: "Scope", Depth: 2, Position: 0, LastRevisedIn: 1},
		{Anchor: "sec-2", Number: "2", Heading: "Model", Depth: 2, Position: 1, LastRevisedIn: 1},
		{Anchor: "sec-2.1", Number: "2.1", Heading: "Detail", Depth: 3, Position: 2, LastRevisedIn: 1},
	}
	if len(secs) != len(want) {
		t.Fatalf("sections = %+v, want %d rows", secs, len(want))
	}
	for i := range want {
		if secs[i] != want[i] {
			t.Errorf("section %d = %+v, want %+v", i, secs[i], want[i])
		}
	}

	// The state_log carries the create, so the timeline can render it.
	entries, err := s.StateLogForEntity(t.Context(), "doc", strconv.FormatInt(doc.ID, 10))
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) != 1 || !strings.Contains(entries[0].Change, `"draft"`) {
		t.Fatalf("state log = %+v, want one draft entry", entries)
	}
}

// TestDocCreateAutoAssignsNumber: 025 §14.3 — a caller who omits Number gets
// the next free one for (project, kind), and it climbs on each subsequent
// create rather than colliding.
func TestDocCreateAutoAssignsNumber(t *testing.T) {
	s := openDocStore(t)

	first := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Slug: "auto-1", Body: specBody, CreatedBy: "stig",
	})
	if first.Number != 1 {
		t.Fatalf("first auto number = %d, want 1", first.Number)
	}

	second := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Slug: "auto-2", Body: specBody, CreatedBy: "stig",
	})
	if second.Number != 2 {
		t.Fatalf("second auto number = %d, want 2", second.Number)
	}

	// An explicit number ahead of the counter is honored; the next auto
	// allocation picks up past it rather than colliding.
	reserved := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 10, Slug: "auto-reserved", Body: specBody, CreatedBy: "stig",
	})
	if reserved.Number != 10 {
		t.Fatalf("reserved number = %d, want 10", reserved.Number)
	}
	third := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Slug: "auto-3", Body: specBody, CreatedBy: "stig",
	})
	if third.Number != 11 {
		t.Fatalf("third auto number = %d, want 11", third.Number)
	}
}

// TestDocCreateAutoAssignsNumberPerKind: spec and ADR draw from separate
// sequences within the same project, per 025 §14.3's "own" per-kind count.
func TestDocCreateAutoAssignsNumberPerKind(t *testing.T) {
	s := openDocStore(t)

	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Slug: "kind-spec", Body: specBody, CreatedBy: "stig",
	})
	adr := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "adr", Slug: "kind-adr", Body: specBody, CreatedBy: "stig",
	})
	if spec.Number != 1 || adr.Number != 1 {
		t.Fatalf("spec/adr numbers = %d/%d, want 1/1 (separate sequences)", spec.Number, adr.Number)
	}
}

// TestDocCreateResolvesEdges: acting-direction frontmatter keys become edge
// rows; a reference this project can resolve points at the doc, one it
// cannot lands verbatim in to_external; inverse keys write nothing.
func TestDocCreateResolvesEdges(t *testing.T) {
	s := openDocStore(t)

	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan",
		Slug: "025-documents-in-the-backbone-2", Body: planBody, CreatedBy: "stig",
	})

	got := docEdges(t, s, plan.ID)
	// Unresolved before resolved within a type: to_doc NULL coalesces to 0.
	want := []model.DocEdge{
		{Type: "covers", ToExternal: "999-nowhere.md#sec-1"},
		{Type: "covers", ToDoc: spec.ID, ToAnchor: "sec-5"},
		{Type: "wasDerivedFrom", ToDoc: spec.ID},
	}
	if len(got) != len(want) {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("edge %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The spec's own `requires` resolved to nothing here (004 is not in this
	// project), so it is external, fragment included.
	specEdges := docEdges(t, s, spec.ID)
	if len(specEdges) != 1 ||
		!reflect.DeepEqual(specEdges[0], model.DocEdge{Type: "requires", ToExternal: "004-execution-backbone.md#sec-6"}) {
		t.Fatalf("spec edges = %+v, want one external requires", specEdges)
	}
}

// TestDocCreateResolvesAnchorMapEdges: amends/replaces are AnchorMaps, so the
// subject anchor becomes from_anchor and "." means document-level. The
// inverse spellings write nothing — one row read backward is the inverse.
func TestDocCreateResolvesAnchorMapEdges(t *testing.T) {
	s := openDocStore(t)

	target := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 4,
		Slug: "004-execution-backbone", Body: specBody, CreatedBy: "stig",
	})

	body := `---
status: draft
amends:
  "#sec-1": 004-execution-backbone.md#sec-2
  ".": 004-execution-backbone.md
amendedBy:
  ".": 026-elsewhere.md
replaces:
  "#sec-2": NO-SPEC
---

# Amending spec

## 1. One {#sec-1}

x

## 2. Two {#sec-2}

y
`
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 30, Slug: "030-amending", Body: body, CreatedBy: "stig",
	})

	got := docEdges(t, s, doc.ID)
	want := []model.DocEdge{
		{Type: "amends", ToDoc: target.ID},
		{Type: "amends", FromAnchor: "sec-1", ToDoc: target.ID, ToAnchor: "sec-2"},
		{Type: "replaces", FromAnchor: "sec-2", ToExternal: "NO-SPEC"},
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

// TestDocCreatePlanHasNoSections: plans carry no anchors and no section rows
// (025 §9), even when their body has headings.
func TestDocCreatePlanHasNoSections(t *testing.T) {
	s := openDocStore(t)

	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan",
		Slug: "025-documents-in-the-backbone-2", Body: planBody, CreatedBy: "stig",
	})
	// 029 §4: the server allocates a plan's number from its project's
	// sequence, so the first plan in a fresh project is 1.
	if plan.Number != 1 {
		t.Errorf("plan number = %d, want 1 (the project's first plan)", plan.Number)
	}
	if secs := docSections(t, s, plan.ID); len(secs) != 0 {
		t.Fatalf("plan sections = %+v, want none", secs)
	}
}

// TestDocCreatePlanWithExplicitNumberReservesIt: an explicit plan number is
// honored like a spec's or ADR's — the rare override, checked for collision —
// and the project's counter advances past it so a later auto-assign never
// retraces it.
func TestDocCreatePlanWithExplicitNumberReservesIt(t *testing.T) {
	s := openDocStore(t)

	reserved := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Number: 5, Slug: "some-plan", Body: planBody, CreatedBy: "stig",
	})
	if reserved.Number != 5 {
		t.Fatalf("plan number = %d, want 5", reserved.Number)
	}

	next := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "next-plan", Body: planBody, CreatedBy: "stig",
	})
	if next.Number != 6 {
		t.Fatalf("plan number = %d, want 6 (past the reserved 5)", next.Number)
	}
}

// TestDocCreateDefaultsAssigneeToCreator: the accept gate is assignee-only, so
// a NULL assignee would make the document unacceptable.
func TestDocCreateDefaultsAssigneeToCreator(t *testing.T) {
	s := openDocStore(t)

	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	if doc.Assignee != "stig" {
		t.Errorf("assignee = %q, want the creator", doc.Assignee)
	}

	explicit := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 26, Slug: "026-y", Body: specBody,
		CreatedBy: "stig", Assignee: "ada",
	})
	if explicit.Assignee != "ada" {
		t.Errorf("assignee = %q, want ada", explicit.Assignee)
	}
}

// TestDocCreateAcceptedPublishesSections: the importer's Status field must
// leave the same state AcceptDoc would, or an imported document would be
// accepted with every anchor unpublished.
func TestDocCreateAcceptedPublishesSections(t *testing.T) {
	s := openDocStore(t)

	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-imported",
		Body: specBody, CreatedBy: "stig", Status: "accepted",
	})
	if doc.Status != "accepted" {
		t.Fatalf("status = %q, want accepted", doc.Status)
	}
	secs := docSections(t, s, doc.ID)
	if len(secs) == 0 {
		t.Fatal("no sections written")
	}
	for _, sec := range secs {
		if !sec.Published {
			t.Errorf("section #%s is unpublished", sec.Anchor)
		}
	}
}

// TestDocCreateAcceptedRejectsTooDeepAnchor: the 025 §6.1 depth gate runs at
// publication, so creating straight at accepted must run it too.
func TestDocCreateAcceptedRejectsTooDeepAnchor(t *testing.T) {
	s := openDocStore(t)
	deep := "---\nstatus: accepted\nissued: 2026-08-01\n---\n\n# T\n\n## 1. Scope {#sec-1}\n\na\n\n" +
		"### 1.1 Sub {#sec-1.1}\n\nb\n\n#### 1.1.1 Deeper {#sec-1.1.1}\n\nc\n"

	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-deep",
		Body: deep, CreatedBy: "stig", Status: "accepted",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "sec-1.1.1") {
		t.Errorf("err = %v, want it to name the offending anchor", err)
	}
}

// TestDocCreateRejectsAnchorDefects: a spec whose anchors are ambiguous or
// disagree with their numbers is unaddressable, so it never lands.
func TestDocCreateRejectsAnchorDefects(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate anchor": "---\nstatus: draft\n---\n\n# T\n\n## 1. A {#sec-1}\n\nx\n\n## 2. B {#sec-1}\n\ny\n",
		"anchor disagrees": "---\nstatus: draft\n---\n\n# T\n\n## 1. A {#sec-9}\n\nx\n",
	} {
		t.Run(name, func(t *testing.T) {
			s := openDocStore(t)
			_, err := createDoc(t, s, DocInput{
				Project: "p1", Kind: "spec", Number: 25, Slug: "025-bad", Body: body, CreatedBy: "stig",
			})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestDocCreateTitleFallsBackToSlug: a body with no H1 still needs a title.
func TestDocCreateTitleFallsBackToSlug(t *testing.T) {
	s := openDocStore(t)

	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "untitled-plan",
		Body: "---\nstatus: draft\n---\n\nNo heading here.\n", CreatedBy: "stig",
	})
	if doc.Title != "untitled-plan" {
		t.Errorf("title = %q, want the slug", doc.Title)
	}
}

// TestDocCreateDedupesEdgesByResolvedTarget: two spellings of one target in
// one frontmatter are one edge. Deduping on the reference text would let both
// through and abort a legal document on doc_edges_unique.
func TestDocCreateDedupesEdgesByResolvedTarget(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	// Four spellings of one target: bare filename, a path to it, the bare
	// corpus number, and 025 §14.3's shorthand.
	body := `---
status: draft
requires:
  - 025-documents-in-the-backbone.md#sec-5
  - docs/specs/025-documents-in-the-backbone.md#sec-5
  - "025"
  - P1-SPEC-25
---

# Requiring plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "requiring-plan", Body: body, CreatedBy: "stig",
	})

	got := docEdges(t, s, plan.ID)
	want := []model.DocEdge{
		{Type: "requires", ToDoc: spec.ID},
		{Type: "requires", ToDoc: spec.ID, ToAnchor: "sec-5"},
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

// TestDocCreateSkipsEmptyRefs: a coverage entry qualified with a level but no
// spec names no target, so it writes no edge — never one with to_external ”.
func TestDocCreateSkipsEmptyRefs(t *testing.T) {
	s := openDocStore(t)
	body := "---\nstatus: draft\ncovers:\n  - coverage: partial\n---\n\n# Plan\n"

	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "empty-covers", Body: body, CreatedBy: "stig",
	})
	if edges := docEdges(t, s, plan.ID); len(edges) != 0 {
		t.Fatalf("edges = %+v, want none", edges)
	}
}

// TestDocCreateDuplicateIsErrDocExists: both unique indexes map onto one
// sentinel, so the API can answer 409 without decoding pgconn.
func TestDocCreateDuplicateIsErrDocExists(t *testing.T) {
	s := openDocStore(t)
	base := DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	}
	mustCreateDoc(t, s, base)

	sameSlug := base
	sameSlug.Number = 26
	if _, err := createDoc(t, s, sameSlug); !errors.Is(err, ErrDocExists) {
		t.Fatalf("duplicate slug err = %v, want ErrDocExists", err)
	}

	sameNumber := base
	sameNumber.Slug = "025-y"
	if _, err := createDoc(t, s, sameNumber); !errors.Is(err, ErrDocExists) {
		t.Fatalf("duplicate (kind, number) err = %v, want ErrDocExists", err)
	}
}

// TestDocCreateWritesPlanBlocksEdge: a plan's document-level `blocks` orders
// it before another plan (025 §5, §9.3), and `blockedBy` says the same thing
// from the other end — the same single row with its ends swapped, so plan-two
// declaring both leaves plan-one blocking it and plan-two blocking plan-three.
// One direction is still all that is stored.
func TestDocCreateWritesPlanBlocksEdge(t *testing.T) {
	s := openDocStore(t)

	one := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-one", Body: planMintBody, CreatedBy: "stig",
	})
	three := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-three", Body: planMintBody, CreatedBy: "stig",
	})
	two := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-two", CreatedBy: "stig",
		Body: "---\nstatus: draft\nblocks: plan-three\nblockedBy: plan-one\n---\n\n# Plan two\n",
	})

	got := docEdges(t, s, two.ID)
	want := []model.DocEdge{{Type: "blocks", ToDoc: three.ID}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("edges of plan-two = %+v, want %+v", got, want)
	}
	// The blockedBy row leaves plan-one, which is where "plan-one blocks
	// plan-two" belongs — plan-two only authored it.
	gotOne := docEdges(t, s, one.ID)
	wantOne := []model.DocEdge{{Type: "blocks", ToDoc: two.ID}}
	if !reflect.DeepEqual(gotOne, wantOne) {
		t.Fatalf("edges of plan-one = %+v, want %+v", gotOne, wantOne)
	}
}

// TestDocCreateAcceptedSupersedesReplaced: creating a document accepted runs
// the same supersession cascade AcceptDoc does, so an importer recording a
// document at the status it actually reached does not leave the document it
// replaces claiming to be current too (WL-133).
func TestDocCreateAcceptedSupersedesReplaced(t *testing.T) {
	s := openDocStore(t)
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Status: "accepted", Body: replacerBody("New", "006-old.md"),
	})

	if got := docStatus(t, s, old.ID); got != "superseded" {
		t.Fatalf("006-old status = %q, want superseded", got)
	}
}

// TestDocCreateAcceptedSupersedesReplacedOutOfOrder: the same corpus imported
// successor-first. The `replaces` edge starts unresolved, so the cascade has
// no row to flip at create time; repointExternalEdges resolves the edge when
// the target arrives and runs the missed cascade there, which is what makes
// the outcome independent of import order (WL-133, on WL-130's mechanism).
func TestDocCreateAcceptedSupersedesReplacedOutOfOrder(t *testing.T) {
	s := openDocStore(t)
	successor := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Status: "accepted", Body: replacerBody("New", "006-old.md"),
	})
	before := docEdges(t, s, successor.ID)
	want := []model.DocEdge{{Type: "replaces", ToExternal: "006-old.md"}}
	if !reflect.DeepEqual(before, want) {
		t.Fatalf("edges before the target exists = %+v, want %+v", before, want)
	}

	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	if got := docStatus(t, s, old.ID); got != "superseded" {
		t.Fatalf("006-old status = %q, want superseded", got)
	}
	// CreateDoc reports the row it landed, not the status it was asked for:
	// this document was superseded on the way in.
	if old.Status != "superseded" {
		t.Errorf("CreateDoc(006-old).Status = %q, want superseded", old.Status)
	}
	after := docEdges(t, s, successor.ID)
	wantAfter := []model.DocEdge{{Type: "replaces", ToDoc: old.ID}}
	if !reflect.DeepEqual(after, wantAfter) {
		t.Fatalf("edges after the target arrives = %+v, want %+v", after, wantAfter)
	}
}

// TestDocCreateAcceptedLeavesDraftTargetAlone: 025 §7's ladder is draft ->
// accepted -> superseded, and a draft pushed straight to superseded is
// reachable by no verb. Neither new cascade path may do it — not the
// accepted-at-create one, and not the one repointExternalEdges runs when the
// edge resolves late.
func TestDocCreateAcceptedLeavesDraftTargetAlone(t *testing.T) {
	t.Run("at create", func(t *testing.T) {
		s := openDocStore(t)
		old := mustCreateDoc(t, s, DocInput{
			Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
			CreatedBy: "stig",
		})
		mustCreateDoc(t, s, DocInput{
			Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
			Status: "accepted", Body: replacerBody("New", "006-old.md"),
		})
		if got := docStatus(t, s, old.ID); got != "draft" {
			t.Fatalf("006-old status = %q, want draft", got)
		}
	})

	t.Run("on the late re-point", func(t *testing.T) {
		s := openDocStore(t)
		mustCreateDoc(t, s, DocInput{
			Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
			Status: "accepted", Body: replacerBody("New", "006-old.md"),
		})
		old := mustCreateDoc(t, s, DocInput{
			Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
			CreatedBy: "stig",
		})
		if got := docStatus(t, s, old.ID); got != "draft" {
			t.Fatalf("006-old status = %q, want draft", got)
		}
		// The draft target is still reachable by the verb that moves it, and
		// accepting it is what finally makes it supersedable.
		if _, _, err := acceptDoc(t, s, old.ID, "stig"); err != nil {
			t.Fatalf("AcceptDoc(006-old): %v", err)
		}
		if got := docStatus(t, s, old.ID); got != "accepted" {
			t.Fatalf("006-old status after accept = %q, want accepted", got)
		}
	})
}

// TestDocCreateDraftReplacerSupersedesNothing: the cascade is acceptance's
// consequence, so the replacing end is guarded too. Here the edge resolves
// late — the case repointExternalEdges now cascades on — but its document is
// still draft, so nothing moves until that document's own accept. (At create
// the guard is structural: a draft create never reaches the cascade.)
func TestDocCreateDraftReplacerSupersedesNothing(t *testing.T) {
	s := openDocStore(t)
	successor := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\nBody.\n",
	})
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	if got := docStatus(t, s, old.ID); got != "accepted" {
		t.Fatalf("006-old status = %q, want accepted: a draft replacer supersedes nothing", got)
	}
	if _, _, err := acceptDoc(t, s, successor.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc(025-new): %v", err)
	}
	if got := docStatus(t, s, old.ID); got != "superseded" {
		t.Fatalf("006-old status after the replacer's accept = %q, want superseded", got)
	}
}

// TestDocCreateRepointsExternalEdges: creating a document re-points the
// project's unresolved references that name it, so the two creation orders
// end in the same edge set.
func TestDocCreateRepointsExternalEdges(t *testing.T) {
	newSpec := func(t *testing.T, s *Store) *model.Doc {
		t.Helper()
		return mustCreateDoc(t, s, DocInput{
			Project: "p1", Kind: "spec", Number: 25,
			Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
		})
	}
	newPlan := func(t *testing.T, s *Store) *model.Doc {
		t.Helper()
		return mustCreateDoc(t, s, DocInput{
			Project: "p1", Kind: "plan", Slug: "025-part-2", Body: planBody, CreatedBy: "stig",
		})
	}
	// 999-nowhere.md names no document here and stays verbatim.
	want := func(specID int64) []model.DocEdge {
		return []model.DocEdge{
			{Type: "covers", ToExternal: "999-nowhere.md#sec-1"},
			{Type: "covers", ToDoc: specID, ToAnchor: "sec-5"},
			{Type: "wasDerivedFrom", ToDoc: specID},
		}
	}

	t.Run("plan then spec", func(t *testing.T) {
		s := openDocStore(t)
		plan := newPlan(t, s)
		for _, e := range docEdges(t, s, plan.ID) {
			if e.ToDoc != 0 {
				t.Fatalf("edge %+v resolved before its target existed", e)
			}
		}
		spec := newSpec(t, s)
		if got := docEdges(t, s, plan.ID); !reflect.DeepEqual(got, want(spec.ID)) {
			t.Fatalf("edges = %+v, want %+v", got, want(spec.ID))
		}
	})

	t.Run("spec then plan", func(t *testing.T) {
		s := openDocStore(t)
		spec := newSpec(t, s)
		plan := newPlan(t, s)
		if got := docEdges(t, s, plan.ID); !reflect.DeepEqual(got, want(spec.ID)) {
			t.Fatalf("edges = %+v, want %+v", got, want(spec.ID))
		}
	})
}

// TestDocCreateRepointDedupesSpellings: several spellings of one target, all
// stored unresolved, collapse onto one edge per (target, anchor) when the
// target arrives — the re-point would otherwise collide with doc_edges_unique.
func TestDocCreateRepointDedupesSpellings(t *testing.T) {
	s := openDocStore(t)
	// Bare filename, a path to it, the bare corpus number and 025 §14.3's
	// shorthand, plus two spellings carrying the same anchor.
	body := `---
status: draft
requires:
  - 025-documents-in-the-backbone.md
  - docs/specs/025-documents-in-the-backbone.md
  - "025"
  - P1-SPEC-25
  - 025-documents-in-the-backbone.md#sec-5
  - 025#sec-5
---

# Requiring plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "requiring-plan", Body: body, CreatedBy: "stig",
	})
	before := docEdges(t, s, plan.ID)
	if len(before) != 6 {
		t.Fatalf("edges before = %+v, want 6 unresolved rows", before)
	}
	for _, e := range before {
		if e.ToExternal == "" {
			t.Fatalf("edge %+v resolved before its target existed", e)
		}
	}

	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	want := []model.DocEdge{
		{Type: "requires", ToDoc: spec.ID},
		{Type: "requires", ToDoc: spec.ID, ToAnchor: "sec-5"},
	}
	if got := docEdges(t, s, plan.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
}

// TestDocCreateRepointIsProjectScoped: re-pointing resolves the way
// resolveDocRef does — same project only — so a document elsewhere naming the
// same filename keeps its verbatim reference.
func TestDocCreateRepointIsProjectScoped(t *testing.T) {
	s := openDocStore(t)
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO projects (id, name, key) VALUES ('p2','P2','P2')`); err != nil {
		t.Fatal(err)
	}
	other := mustCreateDoc(t, s, DocInput{
		Project: "p2", Kind: "plan", Slug: "025-part-2", Body: planBody, CreatedBy: "stig",
	})
	before := docEdges(t, s, other.ID)
	if len(before) == 0 {
		t.Fatal("p2 plan wrote no edges")
	}
	for _, e := range before {
		if e.ToExternal == "" {
			t.Fatalf("edge %+v resolved before its target existed", e)
		}
	}

	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	if got := docEdges(t, s, other.ID); !reflect.DeepEqual(got, before) {
		t.Fatalf("p2 edges = %+v, want %+v unchanged", got, before)
	}
}

// TestDocCreateRepointsCoverageClosure: an unresolvable fullCoverageWith entry
// closes nothing (026 §2.1), so it is re-pointed the same way, in place — the
// (edge_id, position) key does not move.
func TestDocCreateRepointsCoverageClosure(t *testing.T) {
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
      - later-plan.md
---

# Main plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "main-plan", Body: body, CreatedBy: "stig",
	})
	edges := docCoverageEdges(t, s, plan.ID)
	if len(edges) != 1 {
		t.Fatalf("covers edges = %+v, want 1", edges)
	}
	wantBefore := []docCompletedWithRow{
		{position: 0, toExternal: "nowhere-plan.md"},
		{position: 1, toExternal: "later-plan.md"},
	}
	if cw := docCompletedWith(t, s, edges[0].id); !slices.Equal(cw, wantBefore) {
		t.Fatalf("completedWith before = %+v, want %+v", cw, wantBefore)
	}

	later := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "later-plan",
		Body: "---\nstatus: draft\n---\n\n# Later plan\n", CreatedBy: "stig",
	})
	want := []docCompletedWithRow{
		{position: 0, toExternal: "nowhere-plan.md"},
		{position: 1, toDoc: later.ID},
	}
	if cw := docCompletedWith(t, s, edges[0].id); !slices.Equal(cw, want) {
		t.Fatalf("completedWith = %+v, want %+v", cw, want)
	}
}

// TestDocCreateRepointCollapsesDisagreeingCoverage: two unresolvable spellings
// of one section at *different* coverage levels both store, then collapse when
// the target arrives. rebuildEdges would call that a contradiction (026 §5.1),
// but here it lives in another document's frontmatter, so the lower-id row wins
// and this create succeeds rather than wedging an import on an unrelated defect.
func TestDocCreateRepointCollapsesDisagreeingCoverage(t *testing.T) {
	s := openDocStore(t)
	body := `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-1
    coverage: partial
    fullCoverageWith:
      - nowhere-plan.md
  - spec: 025#sec-1
    coverage: full
---

# Contradicting plan
`
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "contradicting-plan", Body: body, CreatedBy: "stig",
	})
	before := docCoverageEdges(t, s, plan.ID)
	if len(before) != 2 {
		t.Fatalf("covers edges before = %+v, want 2 unresolved rows", before)
	}
	// Both are unresolved, so docCoverageEdges' target order does not separate
	// them; the id order is what repointExternalEdges walks.
	slices.SortFunc(before, func(a, b docCoverageEdge) int { return cmp.Compare(a.id, b.id) })

	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	got := docCoverageEdges(t, s, plan.ID)
	want := []docCoverageEdge{{id: got[0].id, toDoc: spec.ID, toAnchor: "sec-1", coverage: "partial"}}
	if !slices.Equal(got, want) {
		t.Fatalf("covers edges = %+v, want %+v", got, want)
	}
	// The survivor is the lower-id row — the partial one, written first — so
	// its closure is the one that stands; the collapsed row's cascaded away.
	if got[0].id != before[0].id {
		t.Fatalf("surviving edge id = %d, want the lower-id row %d", got[0].id, before[0].id)
	}
	wantCW := []docCompletedWithRow{{position: 0, toExternal: "nowhere-plan.md"}}
	if cw := docCompletedWith(t, s, got[0].id); !slices.Equal(cw, wantCW) {
		t.Fatalf("completedWith = %+v, want %+v", cw, wantCW)
	}
	if cw := docCompletedWith(t, s, before[1].id); len(cw) != 0 {
		t.Fatalf("collapsed edge %d kept closure rows %+v", before[1].id, cw)
	}
}
