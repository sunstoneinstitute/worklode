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
