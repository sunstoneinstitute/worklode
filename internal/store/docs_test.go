package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// seedDocsProject inserts the minimal projects row docs rows reference.
func seedDocsProject(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, key) VALUES ('p1','P1','P1')`); err != nil {
		t.Fatal(err)
	}
}

// seedDocsActor inserts the actor docs rows reference as creator/assignee.
func seedDocsActor(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO actors (id, kind) VALUES ($1,'human')`, id); err != nil {
		t.Fatal(err)
	}
}

// openDocStore opens a store with the project and the two actors the doc
// tests write as created_by/assignee.
func openDocStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	seedDocsProject(t, s)
	seedDocsActor(t, s, "stig")
	seedDocsActor(t, s, "ada")
	return s
}

// docEventSeq keeps each test's synthetic event external ids distinct, since
// (source, external_id) is what RecordEvent dedupes on.
var docEventSeq atomic.Int64

// createDoc runs CreateDoc through RecordDocEvent, the way the API will.
func createDoc(t *testing.T, s *Store, in DocInput) (*model.Doc, error) {
	t.Helper()
	var out *model.Doc
	_, _, err := s.RecordDocEvent(t.Context(), "create", "cli",
		fmt.Sprintf("doc-create-%d", docEventSeq.Add(1)), "doc.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, err = CreateDoc(tx, s.Now(), in, eventID)
			return err
		})
	return out, err
}

// mustCreateDoc is createDoc for the cases where creation must succeed.
func mustCreateDoc(t *testing.T, s *Store, in DocInput) *model.Doc {
	t.Helper()
	d, err := createDoc(t, s, in)
	if err != nil {
		t.Fatalf("CreateDoc(%s/%s): %v", in.Project, in.Slug, err)
	}
	return d
}

// updateDocBody runs UpdateDocBody through RecordDocEvent.
func updateDocBody(t *testing.T, s *Store, id int64, body string) (*model.Doc, error) {
	t.Helper()
	var out *model.Doc
	_, _, err := s.RecordDocEvent(t.Context(), "update", "cli",
		fmt.Sprintf("doc-update-%d", docEventSeq.Add(1)), "doc.update", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, err = UpdateDocBody(tx, s.Now(), id, body, eventID)
			return err
		})
	return out, err
}

// docSections reads a document's section rows in position order.
func docSections(t *testing.T, s *Store, docID int64) []model.DocSection {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT anchor, coalesce(number,''), heading, depth, position, last_revised_in, published
		   FROM doc_sections WHERE doc_id = $1 ORDER BY position`, docID)
	if err != nil {
		t.Fatalf("read doc_sections: %v", err)
	}
	defer rows.Close()
	var out []model.DocSection
	for rows.Next() {
		var sec model.DocSection
		if err := rows.Scan(&sec.Anchor, &sec.Number, &sec.Heading, &sec.Depth,
			&sec.Position, &sec.LastRevisedIn, &sec.Published); err != nil {
			t.Fatalf("scan doc_section: %v", err)
		}
		out = append(out, sec)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read doc_sections: %v", err)
	}
	return out
}

// docEdges reads a document's outbound edges in a stable order.
func docEdges(t *testing.T, s *Store, docID int64) []model.DocEdge {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT type, coalesce(from_anchor,''), coalesce(to_doc,0),
		        coalesce(to_anchor,''), coalesce(to_external,'')
		   FROM doc_edges WHERE from_doc = $1
		  ORDER BY type, from_anchor NULLS FIRST, to_doc NULLS LAST, to_external NULLS LAST`, docID)
	if err != nil {
		t.Fatalf("read doc_edges: %v", err)
	}
	defer rows.Close()
	var out []model.DocEdge
	for rows.Next() {
		var e model.DocEdge
		if err := rows.Scan(&e.Type, &e.FromAnchor, &e.ToDoc, &e.ToAnchor, &e.ToExternal); err != nil {
			t.Fatalf("scan doc_edge: %v", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read doc_edges: %v", err)
	}
	return out
}

// specBody is a well-formed spec: frontmatter, an H1 title, and three
// anchored sections whose anchors agree with their numbers.
const specBody = `---
status: draft
issued: 2026-08-01
requires: 004-execution-backbone.md#sec-6
---

# Documents in the backbone

Intro prose.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.

### 2.1 Detail {#sec-2.1}

Detail body.
`

// planBody is a well-formed plan: no anchors, coverage over one resolvable
// spec section and one reference nothing in this project can resolve.
const planBody = `---
status: draft
covers:
  - 025-documents-in-the-backbone.md#sec-5
  - 999-nowhere.md#sec-1
wasDerivedFrom: 025-documents-in-the-backbone.md
---

# Documents in the backbone, part 2

## Task 1

Do the thing.
`

// insertDoc inserts a docs row with the given kind/number/slug, returning
// the generated id and any insert error.
func insertDoc(t *testing.T, s *Store, kind string, number any, slug string) (int64, error) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO docs (project_id, kind, number, slug, title, body, created_at, updated_at)
		 VALUES ('p1', $1, $2, $3, 'title', 'body', $4, $4)
		 RETURNING id`,
		kind, number, slug, now).Scan(&id)
	return id, err
}

func TestDocSchemaSpecRow(t *testing.T) {
	s := openTestStore(t)
	seedDocsProject(t, s)

	id, err := insertDoc(t, s, "spec", 25, "documents-in-the-backbone")
	if err != nil {
		t.Fatalf("insert spec row: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a generated id")
	}
}

func TestDocSchemaPlanRowNumberNull(t *testing.T) {
	s := openTestStore(t)
	seedDocsProject(t, s)

	id, err := insertDoc(t, s, "plan", nil, "documents-in-the-backbone-2")
	if err != nil {
		t.Fatalf("insert plan row with NULL number: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a generated id")
	}
}

func TestDocSchemaSpecRowNumberNullViolatesCheck(t *testing.T) {
	s := openTestStore(t)
	seedDocsProject(t, s)

	_, err := insertDoc(t, s, "spec", nil, "no-number-spec")
	if err == nil {
		t.Fatal("expected CHECK violation, got nil error")
	}
	if !isCheckViolationOn(err, "docs_check") {
		t.Fatalf("expected docs_check CHECK violation, got: %v", err)
	}
}

func TestDocSchemaDuplicateProjectKindNumberViolatesUnique(t *testing.T) {
	s := openTestStore(t)
	seedDocsProject(t, s)

	if _, err := insertDoc(t, s, "spec", 25, "documents-in-the-backbone"); err != nil {
		t.Fatalf("insert first spec row: %v", err)
	}
	_, err := insertDoc(t, s, "spec", 25, "documents-in-the-backbone-dup")
	if err == nil {
		t.Fatal("expected unique violation, got nil error")
	}
	if !isUniqueViolationOn(err, "docs_project_kind_number") {
		t.Fatalf("expected docs_project_kind_number unique violation, got: %v", err)
	}
}

func TestDocSchemaBlocksEdgeWithAnchorViolatesCheck(t *testing.T) {
	s := openTestStore(t)
	seedDocsProject(t, s)

	fromID, err := insertDoc(t, s, "plan", nil, "plan-a")
	if err != nil {
		t.Fatalf("insert from doc: %v", err)
	}
	toID, err := insertDoc(t, s, "plan", nil, "plan-b")
	if err != nil {
		t.Fatalf("insert to doc: %v", err)
	}

	ctx := context.Background()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO doc_edges (from_doc, from_anchor, type, to_doc)
		 VALUES ($1, 'sec-1', 'blocks', $2)`,
		fromID, toID)
	if err == nil {
		t.Fatal("expected CHECK violation, got nil error")
	}
	if !isCheckViolationOn(err, "doc_edges_check1") {
		t.Fatalf("expected doc_edges_check1 CHECK violation, got: %v", err)
	}
}

func TestDocSchemaCoversEdgeSucceeds(t *testing.T) {
	s := openTestStore(t)
	seedDocsProject(t, s)

	planID, err := insertDoc(t, s, "plan", nil, "plan-a")
	if err != nil {
		t.Fatalf("insert plan doc: %v", err)
	}
	specID, err := insertDoc(t, s, "spec", 25, "documents-in-the-backbone")
	if err != nil {
		t.Fatalf("insert spec doc: %v", err)
	}

	ctx := context.Background()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO doc_edges (from_doc, type, to_doc, to_anchor)
		 VALUES ($1, 'covers', $2, 'sec-5')`,
		planID, specID)
	if err != nil {
		t.Fatalf("insert covers edge: %v", err)
	}
}

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
	want := []model.DocEdge{
		{Type: "covers", ToDoc: spec.ID, ToAnchor: "sec-5"},
		{Type: "covers", ToExternal: "999-nowhere.md#sec-1"},
		{Type: "wasDerivedFrom", ToDoc: spec.ID},
	}
	if len(got) != len(want) {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("edge %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The spec's own `requires` resolved to nothing here (004 is not in this
	// project), so it is external, fragment included.
	specEdges := docEdges(t, s, spec.ID)
	if len(specEdges) != 1 ||
		specEdges[0] != (model.DocEdge{Type: "requires", ToExternal: "004-execution-backbone.md#sec-6"}) {
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
		if got[i] != want[i] {
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
	if plan.Number != 0 {
		t.Errorf("plan number = %d, want 0", plan.Number)
	}
	if secs := docSections(t, s, plan.ID); len(secs) != 0 {
		t.Fatalf("plan sections = %+v, want none", secs)
	}
}

// TestDocCreatePlanWithNumberRejected: plans get no shorthand and carry no
// number (025 §14.3); the migration's CHECK only enforces the other half.
func TestDocCreatePlanWithNumberRejected(t *testing.T) {
	s := openDocStore(t)

	_, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Number: 3, Slug: "some-plan", Body: planBody, CreatedBy: "stig",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
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

// TestDocUpdateBodyDraftSpec: a draft spec's body is editable and its
// sections are rebuilt from the new source.
func TestDocUpdateBodyDraftSpec(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	edited := "---\nstatus: draft\n---\n\n# Retitled\n\n## 1. Scope {#sec-1}\n\nnew scope\n"
	updated, err := updateDocBody(t, s, doc.ID, edited)
	if err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}
	if updated.Body != edited {
		t.Error("body not swapped")
	}
	if updated.Title != "Retitled" {
		t.Errorf("title = %q, want Retitled", updated.Title)
	}
	secs := docSections(t, s, doc.ID)
	if len(secs) != 1 || secs[0].Anchor != "sec-1" {
		t.Fatalf("sections = %+v, want only sec-1", secs)
	}
}

// TestDocUpdateBodyPreservesSectionState: an anchor that survives a rebuild
// keeps its published flag and last_revised_in — those are accept-time facts,
// not source facts.
func TestDocUpdateBodyPreservesSectionState(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE doc_sections SET published = true, last_revised_in = 3
		  WHERE doc_id = $1 AND anchor = 'sec-1'`, doc.ID); err != nil {
		t.Fatal(err)
	}

	edited := "---\nstatus: draft\n---\n\n# T\n\n## 1. Scope {#sec-1}\n\nx\n\n## 3. New {#sec-3}\n\ny\n"
	if _, err := updateDocBody(t, s, doc.ID, edited); err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}

	secs := docSections(t, s, doc.ID)
	if len(secs) != 2 {
		t.Fatalf("sections = %+v, want 2", secs)
	}
	if !secs[0].Published || secs[0].LastRevisedIn != 3 {
		t.Errorf("sec-1 = %+v, want published with last_revised_in 3", secs[0])
	}
	if secs[1].Published || secs[1].LastRevisedIn != 1 {
		t.Errorf("sec-3 = %+v, want unpublished at the doc's version", secs[1])
	}
}

// TestDocUpdateBodyAcceptedSpecRejected: an accepted spec is revised, never
// edited in place (025 §9).
func TestDocUpdateBodyAcceptedSpecRejected(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})

	_, err := updateDocBody(t, s, doc.ID, specBody)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "revise") {
		t.Errorf("err = %v, want it to name revise", err)
	}
}

// TestDocUpdateBodyAcceptedPlanAllowed: plans stay freely mutable at any
// status (025 §9, AC6).
func TestDocUpdateBodyAcceptedPlanAllowed(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "a-plan", Body: planBody,
		CreatedBy: "stig", Status: "accepted",
	})

	edited := "---\nstatus: accepted\n---\n\n# A plan\n\nmore tasks\n"
	updated, err := updateDocBody(t, s, doc.ID, edited)
	if err != nil {
		t.Fatalf("UpdateDocBody on an accepted plan: %v", err)
	}
	if updated.Body != edited {
		t.Error("body not swapped")
	}
	if edges := docEdges(t, s, doc.ID); len(edges) != 0 {
		t.Errorf("edges = %+v, want the old ones gone", edges)
	}
}

// TestDocUpdateBodyNotFound covers the unknown-id path.
func TestDocUpdateBodyNotFound(t *testing.T) {
	s := openDocStore(t)
	if _, err := updateDocBody(t, s, 9999, planBody); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestDocGetAndList covers the two read paths and DocFilter's three selectors.
func TestDocGetAndList(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "a-plan", Body: planBody,
		CreatedBy: "stig", Status: "accepted",
	})

	got, err := s.GetDoc(t.Context(), spec.ID)
	if err != nil {
		t.Fatalf("GetDoc: %v", err)
	}
	if got.Slug != "025-x" || got.Body != specBody {
		t.Errorf("GetDoc = %+v, want the spec", got)
	}
	if _, err := s.GetDoc(t.Context(), 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDoc(missing) = %v, want ErrNotFound", err)
	}

	for name, tc := range map[string]struct {
		filter DocFilter
		want   []int64
	}{
		// ListDocs orders by corpus position: kind, then number, then slug.
		"all":         {DocFilter{}, []int64{plan.ID, spec.ID}},
		"by project":  {DocFilter{Project: "p1"}, []int64{plan.ID, spec.ID}},
		"by kind":     {DocFilter{Kind: "plan"}, []int64{plan.ID}},
		"by status":   {DocFilter{Status: "draft"}, []int64{spec.ID}},
		"no matches":  {DocFilter{Project: "nope"}, nil},
		"kind+status": {DocFilter{Kind: "plan", Status: "accepted"}, []int64{plan.ID}},
	} {
		t.Run(name, func(t *testing.T) {
			docs, err := s.ListDocs(t.Context(), tc.filter)
			if err != nil {
				t.Fatalf("ListDocs: %v", err)
			}
			var ids []int64
			for _, d := range docs {
				ids = append(ids, d.ID)
			}
			if fmt.Sprint(ids) != fmt.Sprint(tc.want) {
				t.Fatalf("ids = %v, want %v", ids, tc.want)
			}
		})
	}
}

// TestDocOperationsMetric: RecordDocEvent records the op and its outcome, and
// carries no unbounded label.
func TestDocOperationsMetric(t *testing.T) {
	s := openDocStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)

	if _, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	}); err != nil {
		t.Fatalf("CreateDoc: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("create", "ok")); got != 1 {
		t.Fatalf("doc_operations{create,ok} = %v, want 1", got)
	}

	// A rejected input records the error outcome under the same op.
	if _, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Number: 3, Slug: "bad", Body: planBody, CreatedBy: "stig",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("create", "error")); got != 1 {
		t.Fatalf("doc_operations{create,error} = %v, want 1", got)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "worklode_doc_operations_total" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			for _, lp := range metric.GetLabel() {
				if lp.GetName() != "op" && lp.GetName() != "outcome" {
					t.Fatalf("worklode_doc_operations_total has unexpected label %q", lp.GetName())
				}
			}
		}
	}
}

// TestDocMetricsNilSafe: a store opened without WithMetrics records nothing.
func TestDocMetricsNilSafe(t *testing.T) {
	var m *storeMetrics
	m.docOp("create", nil)
	m.docOp("update", errors.New("boom"))
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
		{Type: "requires", ToDoc: spec.ID, ToAnchor: "sec-5"},
		{Type: "requires", ToDoc: spec.ID},
	}
	if len(got) != len(want) {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
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

// TestDocUpdateBodyKeepsIssued: issued is a lifecycle fact. A plan stays
// mutable at accepted (025 §9), so a body edit that drops the frontmatter key
// must not erase the acceptance date.
func TestDocUpdateBodyKeepsIssued(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "a-plan", CreatedBy: "stig", Status: "accepted",
		Body: "---\nstatus: accepted\nissued: 2026-08-01\n---\n\n# A plan\n",
	})
	if doc.Issued != "2026-08-01" {
		t.Fatalf("issued = %q, want 2026-08-01", doc.Issued)
	}

	updated, err := updateDocBody(t, s, doc.ID,
		"---\nstatus: accepted\n---\n\n# A plan\n\nmore tasks\n")
	if err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}
	if updated.Issued != "2026-08-01" {
		t.Errorf("issued = %q after a body edit that omits it, want it kept", updated.Issued)
	}
}

// TestDocResolveRefShorthand covers 025 §14.3's <KEY>-<TYPE>-<n> form:
// resolution is same-project only, so a reference naming another project's
// key is external even when this project holds that number.
func TestDocResolveRefShorthand(t *testing.T) {
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
	want := []model.DocEdge{
		{Type: "requires", ToDoc: spec.ID, ToAnchor: "sec-2"},
		// This project's key is P1, so ZZ- names a corpus we cannot reach;
		// P1-ADR-25 names a kind this project has no 25 of.
		{Type: "requires", ToExternal: "P1-ADR-25"},
		{Type: "requires", ToExternal: "ZZ-SPEC-25"},
	}
	if len(got) != len(want) {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("edge %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDocResolveRefBareNumberAmbiguous: a project may hold a spec 25 and an
// ADR 25. A bare number cannot say which, so it resolves to neither.
func TestDocResolveRefBareNumberAmbiguous(t *testing.T) {
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
	if edges := docEdges(t, s, ambiguous.ID); len(edges) != 1 || edges[0] != want {
		t.Fatalf("edges = %+v, want %+v", edges, want)
	}
}

// TestDocResolveRefNumberPrefixIsNotANumber: a number-prefixed filename that
// matches no slug is a miss, not spec 025. Resolving on the shared prefix
// would turn "025-…-2.md" into an edge to spec 025 — a wrong edge is worse
// than an unresolved one.
func TestDocResolveRefNumberPrefixIsNotANumber(t *testing.T) {
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
	if edges := docEdges(t, s, plan.ID); len(edges) != 1 || edges[0] != want {
		t.Fatalf("edges = %+v, want %+v", edges, want)
	}
}

// --- editorial lifecycle (025 §6, §7) ---------------------------------------

// acceptDoc runs AcceptDoc through RecordDocEvent, the way the API will.
func acceptDoc(t *testing.T, s *Store, id int64, actor string) (*model.Doc, error) {
	t.Helper()
	var out *model.Doc
	_, _, err := s.RecordDocEvent(t.Context(), "accept", "cli",
		fmt.Sprintf("doc-accept-%d", docEventSeq.Add(1)), "doc.accept", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, err = AcceptDoc(tx, s.Now(), id, actor, eventID)
			return err
		})
	return out, err
}

// reviseDoc runs ReviseDoc through RecordDocEvent.
func reviseDoc(t *testing.T, s *Store, id int64, actor string) error {
	t.Helper()
	_, _, err := s.RecordDocEvent(t.Context(), "revise", "cli",
		fmt.Sprintf("doc-revise-%d", docEventSeq.Add(1)), "doc.revise", nil,
		func(tx *sql.Tx, eventID int64) error {
			return ReviseDoc(tx, s.Now(), id, actor, eventID)
		})
	return err
}

// updateRevision runs UpdateRevision through RecordDocEvent.
func updateRevision(t *testing.T, s *Store, id int64, body string) error {
	t.Helper()
	_, _, err := s.RecordDocEvent(t.Context(), "revise", "cli",
		fmt.Sprintf("doc-revision-edit-%d", docEventSeq.Add(1)), "doc.revision.update", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UpdateRevision(tx, s.Now(), id, body, eventID)
		})
	return err
}

// acceptRevision runs AcceptRevision through RecordDocEvent.
func acceptRevision(t *testing.T, s *Store, id int64, actor string) (*model.Doc, error) {
	t.Helper()
	var out *model.Doc
	_, _, err := s.RecordDocEvent(t.Context(), "accept", "cli",
		fmt.Sprintf("doc-accept-revision-%d", docEventSeq.Add(1)), "doc.revision.accept", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, err = AcceptRevision(tx, s.Now(), id, actor, eventID)
			return err
		})
	return out, err
}

// mustAcceptedSpec creates a draft spec and accepts it, the starting state
// every revision test needs.
func mustAcceptedSpec(t *testing.T, s *Store, slug string) *model.Doc {
	t.Helper()
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: slug, Body: specBody, CreatedBy: "stig",
	})
	accepted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc(%s): %v", slug, err)
	}
	return accepted
}

// revisedSpecBody is specBody with sec-2's body edited and a letter-suffix
// insert added: exactly one Changed anchor and one Added one (025 §3, §6).
const revisedSpecBody = `---
status: accepted
issued: 2026-08-01
requires: 004-execution-backbone.md#sec-6
---

# Documents in the backbone

Intro prose.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body, revised.

### 2.1 Detail {#sec-2.1}

Detail body.

## 2a. Inserted {#sec-2a}

Inserted body.
`

// TestDocAcceptDraftSpec: the assignee's accept flips the status, freezes the
// published anchor set, and lands in the state log.
func TestDocAcceptDraftSpec(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	accepted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Errorf("status = %q, want accepted", accepted.Status)
	}
	if accepted.Version != 1 {
		t.Errorf("version = %d, want 1", accepted.Version)
	}
	for _, sec := range docSections(t, s, doc.ID) {
		if !sec.Published {
			t.Errorf("section %s not published", sec.Anchor)
		}
		if sec.LastRevisedIn != 1 {
			t.Errorf("section %s last_revised_in = %d, want 1", sec.Anchor, sec.LastRevisedIn)
		}
	}

	entries, err := s.StateLogForEntity(t.Context(), "doc", strconv.FormatInt(doc.ID, 10))
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) != 2 || !strings.Contains(entries[1].Change, `"accepted"`) {
		t.Fatalf("state log = %+v, want a second, accepted entry", entries)
	}
}

// TestDocAcceptWrongActorForbidden: acceptance is the assignee's act (025 §7).
func TestDocAcceptWrongActorForbidden(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	_, err := acceptDoc(t, s, doc.ID, "ada")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if !strings.Contains(err.Error(), "stig") {
		t.Errorf("err = %v, want it to name the assignee", err)
	}
	if got, err := s.GetDoc(t.Context(), doc.ID); err != nil || got.Status != "draft" {
		t.Fatalf("doc = %+v, %v; want it still draft", got, err)
	}
}

// TestDocAcceptAlreadyAccepted: accept is a draft-only transition.
func TestDocAcceptAlreadyAccepted(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")

	if _, err := acceptDoc(t, s, doc.ID, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocAcceptPlanRejected: plan acceptance mints the plan's tasks in the
// same transaction (025 §9.2), which part 3 supplies — an accepted plan with
// no tasks must never exist, so the stub refuses.
func TestDocAcceptPlanRejected(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "a-plan", Body: planBody, CreatedBy: "stig",
	})

	_, err := acceptDoc(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "task") {
		t.Errorf("err = %v, want it to name task minting", err)
	}
}

// TestDocAcceptSupersedesReplacedDoc: a document-level replaces edge flips its
// target in the same transaction (025 §3.3).
func TestDocAcceptSupersedesReplacedDoc(t *testing.T) {
	s := openDocStore(t)
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	body := "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md\n---\n\n" +
		"# New\n\n## 1. Scope {#sec-1}\n\na\n"
	newDoc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", Body: body, CreatedBy: "stig",
	})

	if _, err := acceptDoc(t, s, newDoc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	got, err := s.GetDoc(t.Context(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "superseded" {
		t.Errorf("replaced doc status = %q, want superseded", got.Status)
	}
	entries, err := s.StateLogForEntity(t.Context(), "doc", strconv.FormatInt(old.ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !strings.Contains(entries[1].Change, `"superseded"`) {
		t.Fatalf("state log = %+v, want a superseded entry on the replaced doc", entries)
	}
}

// TestDocAcceptSectionScopedReplacesDoesNotSupersede: section-level
// supersession stays derived (025 §3.3), so it flips no document.
func TestDocAcceptSectionScopedReplacesDoesNotSupersede(t *testing.T) {
	s := openDocStore(t)
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	body := "---\nstatus: draft\nreplaces:\n  \"#sec-1\":\n    - 006-old.md#sec-2\n---\n\n" +
		"# New\n\n## 1. Scope {#sec-1}\n\na\n"
	newDoc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", Body: body, CreatedBy: "stig",
	})

	if _, err := acceptDoc(t, s, newDoc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	got, err := s.GetDoc(t.Context(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "accepted" {
		t.Errorf("replaced doc status = %q, want it untouched", got.Status)
	}
}

// TestDocAcceptRejectsTooDeepAnchor: the depth limit is evaluated at
// publication (025 §6 rule 6), so a first accept enforces it too.
func TestDocAcceptRejectsTooDeepAnchor(t *testing.T) {
	s := openDocStore(t)
	deep := "---\nstatus: draft\n---\n\n# T\n\n## 1. Scope {#sec-1}\n\na\n\n" +
		"### 1.1 Sub {#sec-1.1}\n\nb\n\n#### 1.1.1 Deeper {#sec-1.1.1}\n\nc\n"
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-deep", Body: deep, CreatedBy: "stig",
	})

	_, err := acceptDoc(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "sec-1.1.1") {
		t.Errorf("err = %v, want it to name the offending anchor", err)
	}
}

// TestDocAcceptAllowsAnchorAtTheDepthLimit: level 3 is addressable; only
// deeper headings are content within their nearest anchored ancestor.
func TestDocAcceptAllowsAnchorAtTheDepthLimit(t *testing.T) {
	s := openDocStore(t)
	body := "---\nstatus: draft\n---\n\n# T\n\n## 1. Scope {#sec-1}\n\na\n\n" +
		"### 1.1 Sub {#sec-1.1}\n\nb\n"
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-ok", Body: body, CreatedBy: "stig",
	})

	if _, err := acceptDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
}

// TestDocAcceptNotFound covers the unknown-id path.
func TestDocAcceptNotFound(t *testing.T) {
	s := openDocStore(t)
	if _, err := acceptDoc(t, s, 9999, "stig"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestDocReviseOpensOneCandidate: a revision copies the accepted body to edit,
// and a second open revision is refused (025 §7.2, one candidate per doc).
func TestDocReviseOpensOneCandidate(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")

	if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	rev, err := s.GetDocRevision(t.Context(), doc.ID)
	if err != nil {
		t.Fatalf("GetDocRevision: %v", err)
	}
	if rev.Body != specBody {
		t.Error("candidate body is not a copy of the accepted body")
	}
	if rev.CreatedBy != "ada" {
		t.Errorf("created_by = %q, want ada", rev.CreatedBy)
	}

	if err := reviseDoc(t, s, doc.ID, "stig"); !errors.Is(err, ErrRevisionExists) {
		t.Fatalf("second ReviseDoc err = %v, want ErrRevisionExists", err)
	}
}

// TestDocRevisePlanRejected: plans are edited in place (025 §9), never revised.
func TestDocRevisePlanRejected(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "a-plan", Body: planBody,
		CreatedBy: "stig", Status: "accepted",
	})

	err := reviseDoc(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "in place") {
		t.Errorf("err = %v, want it to say plans are edited in place", err)
	}
}

// TestDocReviseDraftRejected: a draft is edited in place — there is no
// accepted version to revise against.
func TestDocReviseDraftRejected(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	if err := reviseDoc(t, s, doc.ID, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocUpdateRevision: the candidate body is editable, and a malformed one
// is refused before it can reach the accept gate.
func TestDocUpdateRevision(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}

	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	rev, err := s.GetDocRevision(t.Context(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Body != revisedSpecBody {
		t.Error("candidate body not swapped")
	}
	if got, err := s.GetDoc(t.Context(), doc.ID); err != nil || got.Body != specBody {
		t.Fatal("the accepted body must stay authoritative throughout (025 §7.2)")
	}

	bad := "---\nstatus: draft\n---\n\n# T\n\n## 1. A {#sec-1}\n\na\n\n## 2. B {#sec-1}\n\nb\n"
	if err := updateRevision(t, s, doc.ID, bad); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput on a duplicate anchor", err)
	}
}

// TestDocUpdateRevisionWithoutOpenRevision: nothing to edit is ErrNotFound.
func TestDocUpdateRevisionWithoutOpenRevision(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")

	if err := updateRevision(t, s, doc.ID, revisedSpecBody); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestDocAcceptRevisionRejectsRemovedPublishedAnchor: the one invariant that
// survives into draft (025 §7.2) — an anchor the accepted version published
// may not disappear.
func TestDocAcceptRevisionRejectsRemovedPublishedAnchor(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	shortened := "---\nstatus: accepted\n---\n\n# Documents in the backbone\n\n" +
		"## 1. Scope {#sec-1}\n\nScope body.\n\n## 2. Model {#sec-2}\n\nModel body.\n"
	if err := updateRevision(t, s, doc.ID, shortened); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	_, err := acceptRevision(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "sec-2.1") || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("err = %v, want the SectionDiff violation naming sec-2.1", err)
	}
	if got, err := s.GetDoc(t.Context(), doc.ID); err != nil || got.Version != 1 {
		t.Fatalf("doc = %+v, %v; want the accepted version untouched", got, err)
	}
}

// TestDocAcceptRevisionRejectsRenumber: anchors are immutable, so an accepted
// section is never renumbered (025 §6 rule 3).
func TestDocAcceptRevisionRejectsRenumber(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	// sec-2 renumbered to 3 — lintAnchors would reject "## 3. … {#sec-2}", so
	// the renumber arrives the only way it can: with the anchor dropped and a
	// new one taking its place. sec-2 then reads as removed, which is the same
	// rule seen from the other side.
	renumbered := strings.Replace(revisedSpecBody, "## 2. Model {#sec-2}", "## 3. Model {#sec-3}", 1)
	if err := updateRevision(t, s, doc.ID, renumbered); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	_, err := acceptRevision(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "sec-2") {
		t.Errorf("err = %v, want it to name sec-2", err)
	}
}

// TestDocAcceptRevisionAllowsUnpublishedAnchorRemoval: the append-only gate
// protects anchors the accepted version published (025 §7.2), not every row.
// An unpublished anchor on an accepted document is what a corpus import
// leaves behind, and dropping one is legal.
func TestDocAcceptRevisionAllowsUnpublishedAnchorRemoval(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE doc_sections SET published = false WHERE doc_id = $1 AND anchor = 'sec-2.1'`,
		doc.ID); err != nil {
		t.Fatal(err)
	}
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	shortened := "---\nstatus: accepted\n---\n\n# Documents in the backbone\n\n" +
		"## 1. Scope {#sec-1}\n\nScope body.\n\n## 2. Model {#sec-2}\n\nModel body.\n"
	if err := updateRevision(t, s, doc.ID, shortened); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	updated, err := acceptRevision(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("version = %d, want 2", updated.Version)
	}
	if secs := docSections(t, s, doc.ID); len(secs) != 2 {
		t.Errorf("sections = %+v, want sec-2.1 gone", secs)
	}
}

// TestDocAcceptRevision: the clean path — body swapped, version bumped,
// last_revised_in stamped on exactly the changed anchor, the insert published
// from this version, and the candidate row consumed.
func TestDocAcceptRevision(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	updated, err := acceptRevision(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}
	if updated.Body != revisedSpecBody {
		t.Error("body not swapped")
	}
	if updated.Version != 2 {
		t.Errorf("version = %d, want 2", updated.Version)
	}
	if updated.Status != "accepted" {
		t.Errorf("status = %q, want accepted", updated.Status)
	}

	want := map[string]int{"sec-1": 1, "sec-2": 2, "sec-2.1": 1, "sec-2a": 2}
	secs := docSections(t, s, doc.ID)
	if len(secs) != len(want) {
		t.Fatalf("sections = %+v, want %d", secs, len(want))
	}
	for _, sec := range secs {
		if !sec.Published {
			t.Errorf("section %s not published", sec.Anchor)
		}
		if sec.LastRevisedIn != want[sec.Anchor] {
			t.Errorf("section %s last_revised_in = %d, want %d",
				sec.Anchor, sec.LastRevisedIn, want[sec.Anchor])
		}
	}

	if _, err := s.GetDocRevision(t.Context(), doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDocRevision err = %v, want the candidate consumed", err)
	}
}

// TestDocAcceptRevisionWrongActorForbidden: the revision accept is gated like
// the first one.
func TestDocAcceptRevisionWrongActorForbidden(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	if _, err := acceptRevision(t, s, doc.ID, "ada"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// TestDocAcceptRevisionWithoutOpenRevision: nothing to accept is ErrNotFound.
func TestDocAcceptRevisionWithoutOpenRevision(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")

	if _, err := acceptRevision(t, s, doc.ID, "stig"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestDocAcceptRevisionSupersedesReplacedDoc: a replaces edge added by the
// revision takes effect when the revision lands, not before.
func TestDocAcceptRevisionSupersedesReplacedDoc(t *testing.T) {
	s := openDocStore(t)
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	withReplaces := strings.Replace(revisedSpecBody,
		"requires: 004-execution-backbone.md#sec-6",
		"requires: 004-execution-backbone.md#sec-6\nreplaces:\n  \".\":\n    - 006-old.md", 1)
	if err := updateRevision(t, s, doc.ID, withReplaces); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	if got, err := s.GetDoc(t.Context(), old.ID); err != nil || got.Status != "accepted" {
		t.Fatalf("doc = %+v, %v; want the target untouched until the revision lands", got, err)
	}

	if _, err := acceptRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}
	got, err := s.GetDoc(t.Context(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "superseded" {
		t.Errorf("replaced doc status = %q, want superseded", got.Status)
	}
}
