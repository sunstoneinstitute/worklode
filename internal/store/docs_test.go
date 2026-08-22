package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
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

// replaceDocEdges runs ReplaceDocEdges through RecordDocEvent.
func replaceDocEdges(t *testing.T, s *Store, id int64) error {
	t.Helper()
	_, _, err := s.RecordDocEvent(t.Context(), "edges", "cli",
		fmt.Sprintf("doc-edges-%d", docEventSeq.Add(1)), "doc.edges_rebuilt", nil,
		func(tx *sql.Tx, eventID int64) error {
			return ReplaceDocEdges(tx, s.Now(), id, eventID)
		})
	return err
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

// docEdges reads a document's outbound edges in ListDocEdges' order — the
// same expression, so these tests exercise the order production serves rather
// than a second one that could disagree about where a NULL sorts.
func docEdges(t *testing.T, s *Store, docID int64) []model.DocEdge {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT type, coalesce(from_anchor,''), coalesce(to_doc,0),
		        coalesce(to_anchor,''), coalesce(to_external,'')
		   FROM doc_edges WHERE from_doc = $1
		  ORDER BY type, coalesce(from_anchor,''), coalesce(to_doc,0),
		           coalesce(to_anchor,''), coalesce(to_external,'')`, docID)
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

// docCoverageEdge is one covers edge id and level, for tests exercising
// 026 §2.1's three-valued coverage that model.DocEdge's plain columns do not
// carry.
type docCoverageEdge struct {
	id       int64
	toDoc    int64
	toAnchor string
	coverage string
}

// docCoverageEdges reads a document's outbound covers edges with their
// ids and levels, ordered by target so tests can address them positionally.
func docCoverageEdges(t *testing.T, s *Store, docID int64) []docCoverageEdge {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT id, coalesce(to_doc,0), coalesce(to_anchor,''), coalesce(coverage,'')
		   FROM doc_edges WHERE from_doc = $1 AND type = 'covers'
		  ORDER BY coalesce(to_doc,0), coalesce(to_anchor,'')`, docID)
	if err != nil {
		t.Fatalf("read covers edges: %v", err)
	}
	defer rows.Close()
	var out []docCoverageEdge
	for rows.Next() {
		var e docCoverageEdge
		if err := rows.Scan(&e.id, &e.toDoc, &e.toAnchor, &e.coverage); err != nil {
			t.Fatalf("scan covers edge: %v", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read covers edges: %v", err)
	}
	return out
}

// docCompletedWithRow is one doc_coverage_completed_with row.
type docCompletedWithRow struct {
	position   int
	toDoc      int64
	toExternal string
}

// docCompletedWith reads a covers edge's fullCoverageWith closure in
// authored order.
func docCompletedWith(t *testing.T, s *Store, edgeID int64) []docCompletedWithRow {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT position, coalesce(to_doc,0), coalesce(to_external,'')
		   FROM doc_coverage_completed_with WHERE edge_id = $1 ORDER BY position`, edgeID)
	if err != nil {
		t.Fatalf("read doc_coverage_completed_with: %v", err)
	}
	defer rows.Close()
	var out []docCompletedWithRow
	for rows.Next() {
		var r docCompletedWithRow
		if err := rows.Scan(&r.position, &r.toDoc, &r.toExternal); err != nil {
			t.Fatalf("scan doc_coverage_completed_with: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read doc_coverage_completed_with: %v", err)
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

// planMintBody is a well-formed plan in the mintable ## Tasks format
// (025 §9.1): three definitions, Task 2 blocked by Task 1.
const planMintBody = `---
status: draft
---

# A mintable plan

## Tasks

### Task 1 — First task

` + "```yaml" + `
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: []
` + "```" + `

Do the first thing.

### Task 2 — Second task

` + "```yaml" + `
kind: bug
priority: medium
blockedBy: [1]
` + "```" + `

Do the second thing.

### Task 3 — Third task

` + "```yaml" + `
kind: chore
priority: low
blockedBy: []
` + "```" + `

Do the third thing.
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
	if !isCheckViolationOn(err, "docs_number_matches_kind") {
		t.Fatalf("expected docs_number_matches_kind CHECK violation, got: %v", err)
	}
}

// The other half of 025 §14.3, enforced by the schema since migration 0037:
// a plan carries no corpus number even for a writer going around the store.
func TestDocSchemaPlanRowWithNumberViolatesCheck(t *testing.T) {
	s := openTestStore(t)
	seedDocsProject(t, s)

	_, err := insertDoc(t, s, "plan", 25, "numbered-plan")
	if err == nil {
		t.Fatal("expected CHECK violation, got nil error")
	}
	if !isCheckViolationOn(err, "docs_number_matches_kind") {
		t.Fatalf("expected docs_number_matches_kind CHECK violation, got: %v", err)
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
		`INSERT INTO doc_edges (from_doc, type, to_doc, to_anchor, coverage, declared_by)
		 VALUES ($1, 'covers', $2, 'sec-5', 'full', $1)`,
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

// TestDocUpdateBodySameSecondIsDistinguishable: a draft spec/ADR keeps its
// version across a body edit (025 §7), so updated_at is the only externally
// visible signal that a second edit landed. Two edits inside the same
// wall-clock second, changing neither title nor issued, must still produce
// distinguishable updated_at values (WL-285) rather than collapsing into an
// update with no observable trace.
func TestDocUpdateBodySameSecondIsDistinguishable(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	// Same second, different instants: this is what "landed in the same
	// wall-clock second" means, not two calls sharing one identical now().
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	editedOnce := "---\nstatus: draft\nissued: 2026-08-01\n---\n\n# Documents in the backbone\n\n## 1. Scope {#sec-1}\n\nfirst edit\n"
	editedTwice := "---\nstatus: draft\nissued: 2026-08-01\n---\n\n# Documents in the backbone\n\n## 1. Scope {#sec-1}\n\nsecond edit\n"

	s.SetNowFunc(func() time.Time { return base.Add(100 * time.Millisecond) })
	first, err := updateDocBody(t, s, doc.ID, editedOnce)
	if err != nil {
		t.Fatalf("UpdateDocBody (first): %v", err)
	}

	s.SetNowFunc(func() time.Time { return base.Add(900 * time.Millisecond) })
	second, err := updateDocBody(t, s, doc.ID, editedTwice)
	if err != nil {
		t.Fatalf("UpdateDocBody (second): %v", err)
	}

	if !first.UpdatedAt.Truncate(time.Second).Equal(second.UpdatedAt.Truncate(time.Second)) {
		t.Fatalf("test setup invalid: updated_at values are not in the same wall-clock second: %s, %s",
			first.UpdatedAt, second.UpdatedAt)
	}
	if first.Title != second.Title || first.Issued != second.Issued {
		t.Fatalf("test setup invalid: title/issued moved between edits: %+v, %+v", first, second)
	}
	if first.Version != second.Version {
		t.Fatalf("test setup invalid: version moved between edits: %d, %d", first.Version, second.Version)
	}
	if first.UpdatedAt.Equal(second.UpdatedAt) {
		t.Errorf("updated_at = %s for both edits; a second edit in the same wall-clock second left no trace",
			first.UpdatedAt)
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

// TestResolveDocRef covers the ref grammar the doc verbs take: an id, a slug,
// a slug nobody holds, and a slug two projects hold.
func TestResolveDocRef(t *testing.T) {
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
	if got := docEdges(t, s, plan.ID); !slices.Equal(got, want) {
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

// TestDocOperationsMetric: RecordDocEvent records the op and its outcome, and
// carries no unbounded label.
func TestDocOperationsMetric(t *testing.T) {
	s := openDocStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)

	created, err := createDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	if err != nil {
		t.Fatalf("CreateDoc: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("create", "ok")); got != 1 {
		t.Fatalf("doc_operations{create,ok} = %v, want 1", got)
	}

	// The importer's re-resolution pass records under its own fixed verb.
	if err := replaceDocEdges(t, s, created.ID); err != nil {
		t.Fatalf("ReplaceDocEdges: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("edges", "ok")); got != 1 {
		t.Fatalf("doc_operations{edges,ok} = %v, want 1", got)
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

	mfs, gatherErr := reg.Gather()
	if gatherErr != nil {
		t.Fatalf("gather: %v", gatherErr)
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
		{Type: "requires", ToDoc: spec.ID},
		{Type: "requires", ToDoc: spec.ID, ToAnchor: "sec-5"},
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

// TestDocCoverageLevels: a full, a partial with a resolvable
// fullCoverageWith, and a none entry each land with their authored level on
// the covers edge, and only the partial entry writes a
// doc_coverage_completed_with row (026 §2.1, §5).
func TestDocCoverageLevels(t *testing.T) {
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
	if len(got) != 1 || got[0] != want[0] {
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
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
}

// TestDocSchemaDefersEdgeSucceeds: migration 0045 admits 'defers' to
// doc_edges' type CHECK.
func TestDocSchemaDefersEdgeSucceeds(t *testing.T) {
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

// TestDocResolveRefShorthand covers 025 §14.3's <KEY>-<TYPE>-<n> form against
// the referring document's own corpus: the key must be a real project's, and
// the type token is verified against the target's kind rather than trusted.
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
		if got[i] != want[i] {
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
		if got[i] != want[i] {
			t.Errorf("edge %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestProjectKeySpecAndADRReserved: SPEC and ADR are the <TYPE> token of the
// document shorthand, which resolves on the project key alone (025 §14.3), so
// the projects_key_format CHECK rejects them as keys.
func TestProjectKeySpecAndADRReserved(t *testing.T) {
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

// acceptDoc runs AcceptDoc through RecordDocEvent, the way the API will. The
// second return is the minted task set (nil for a spec or ADR accept).
func acceptDoc(t *testing.T, s *Store, id int64, actor string) (*model.Doc, []model.Task, error) {
	t.Helper()
	var out *model.Doc
	var minted []model.Task
	_, _, err := s.RecordDocEvent(t.Context(), "accept", "cli",
		fmt.Sprintf("doc-accept-%d", docEventSeq.Add(1)), "doc.accept", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, minted, err = AcceptDoc(tx, s.Now(), id, actor, eventID)
			return err
		})
	return out, minted, err
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

// discardRevision runs DiscardRevision through RecordDocEvent.
func discardRevision(t *testing.T, s *Store, id int64, actor string) (*model.Doc, error) {
	t.Helper()
	var out *model.Doc
	_, _, err := s.RecordDocEvent(t.Context(), "discard", "cli",
		fmt.Sprintf("doc-revision-discard-%d", docEventSeq.Add(1)), "doc.revision_discarded", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, err = DiscardRevision(tx, s.Now(), id, actor, eventID)
			return err
		})
	return out, err
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
	accepted, _, err := acceptDoc(t, s, doc.ID, "stig")
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

	accepted, _, err := acceptDoc(t, s, doc.ID, "stig")
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

	_, _, err := acceptDoc(t, s, doc.ID, "ada")
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

	if _, _, err := acceptDoc(t, s, doc.ID, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocAcceptPlanRejected: a plan whose body defines no ## Tasks section
// refuses to accept — PlanTasks's error surfaces as ErrInvalidInput, and an
// accepted plan with no tasks must never exist (025 §9.2).
func TestDocAcceptPlanRejected(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "a-plan", Body: planBody, CreatedBy: "stig",
	})

	_, _, err := acceptDoc(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "task") {
		t.Errorf("err = %v, want it to name task minting", err)
	}
}

// countTasksWithPlanDoc counts the tasks.plan_doc rows pointing at docID.
func countTasksWithPlanDoc(t *testing.T, s *Store, docID int64) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE plan_doc = $1`, docID).Scan(&n); err != nil {
		t.Fatalf("count tasks with plan_doc %d: %v", docID, err)
	}
	return n
}

// TestDocAcceptPlanMintsTasks: accepting a plan mints one draft task per
// ## Tasks definition, in the plan's project, carrying plan_doc, title, body,
// kind, priority and skills from its definition and created_by the accepting
// actor — and nothing above them: no child_of edge is written for any of
// them.
func TestDocAcceptPlanMintsTasks(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "mint-plan", Body: planMintBody, CreatedBy: "stig",
	})

	accepted, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Errorf("status = %q, want accepted", accepted.Status)
	}
	if len(minted) != 3 {
		t.Fatalf("minted %d tasks, want 3", len(minted))
	}

	wantTitles := []string{"First task", "Second task", "Third task"}
	wantKinds := []string{"feature", "bug", "chore"}
	wantPriorities := []string{"high", "medium", "low"}
	for i, task := range minted {
		if task.State != "draft" {
			t.Errorf("minted task %d state = %q, want draft", i, task.State)
		}
		if task.Project != "p1" {
			t.Errorf("minted task %d project = %q, want p1", i, task.Project)
		}
		if task.Title != wantTitles[i] {
			t.Errorf("minted task %d title = %q, want %q", i, task.Title, wantTitles[i])
		}
		if task.Kind != wantKinds[i] {
			t.Errorf("minted task %d kind = %q, want %q", i, task.Kind, wantKinds[i])
		}
		if task.Priority != wantPriorities[i] {
			t.Errorf("minted task %d priority = %q, want %q", i, task.Priority, wantPriorities[i])
		}
		if task.CreatedBy != "stig" {
			t.Errorf("minted task %d created_by = %q, want stig", i, task.CreatedBy)
		}
	}
	if !strings.Contains(minted[0].Body, "Do the first thing.") {
		t.Errorf("minted task 0 body = %q, missing prose", minted[0].Body)
	}
	if len(minted[0].Skills) != 1 || minted[0].Skills[0] != "superpowers:test-driven-development" {
		t.Errorf("minted task 0 skills = %v, want [superpowers:test-driven-development]", minted[0].Skills)
	}

	for _, task := range minted {
		var planDoc sql.NullInt64
		if err := s.db.QueryRow(`SELECT plan_doc FROM tasks WHERE id = $1`, task.ID).Scan(&planDoc); err != nil {
			t.Fatalf("read plan_doc of %s: %v", task.ID, err)
		}
		if !planDoc.Valid || planDoc.Int64 != doc.ID {
			t.Errorf("task %s plan_doc = %v, want %d", task.ID, planDoc, doc.ID)
		}
	}

	// Nothing above the minted tasks: no child_of edge involves any of them.
	for _, task := range minted {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM task_edges WHERE type = 'child_of' AND (from_task = $1 OR to_task = $1)`,
			task.ID).Scan(&n); err != nil {
			t.Fatalf("count child_of edges of %s: %v", task.ID, err)
		}
		if n != 0 {
			t.Errorf("task %s has %d child_of edges, want none", task.ID, n)
		}
	}
}

// TestDocAcceptPlanInvariant: before accept, no task carries the plan's id;
// after, the count equals the definition count; a second accept of the same
// body mints nothing, so the set can never double-mint (025 §9.2 AC2).
func TestDocAcceptPlanInvariant(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "invariant-plan", Body: planMintBody, CreatedBy: "stig",
	})

	if before := countTasksWithPlanDoc(t, s, doc.ID); before != 0 {
		t.Fatalf("tasks with plan_doc before accept = %d, want 0", before)
	}

	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	after := countTasksWithPlanDoc(t, s, doc.ID)
	if after != len(minted) {
		t.Fatalf("tasks with plan_doc after accept = %d, want %d", after, len(minted))
	}

	// Re-accepting an unedited plan is a no-op, not a refusal: every
	// declaration already has a row, so there is nothing to mint (025 §9.2).
	_, again, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("second accept: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second accept minted %d tasks, want none", len(again))
	}
	if got := countTasksWithPlanDoc(t, s, doc.ID); got != after {
		t.Fatalf("tasks with plan_doc after the second accept = %d, want unchanged %d", got, after)
	}
}

// TestDocAcceptPlanBlockedByMintsBlocksEdge: blockedBy: [1] on Task 2's
// definition yields a blocks edge from minted task 1 to minted task 2; the
// blocked task is absent from the ready set until task 1 closes, using the
// existing blockedCondition — no new machinery here, and no plan-to-plan gate
// (that is a later task).
func TestDocAcceptPlanBlockedByMintsBlocksEdge(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "blocked-plan", Body: planMintBody, CreatedBy: "stig",
	})

	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	task1, task2 := minted[0].ID, minted[1].ID

	var edgeType string
	err = s.db.QueryRow(
		`SELECT type FROM task_edges WHERE from_task = $1 AND to_task = $2`, task1, task2,
	).Scan(&edgeType)
	if err != nil {
		t.Fatalf("read edge %s -> %s: %v", task1, task2, err)
	}
	if edgeType != "blocks" {
		t.Fatalf("edge %s -> %s type = %q, want blocks", task1, task2, edgeType)
	}

	// Promote both minted tasks out of draft so the ready-set check exercises
	// blockedCondition rather than the draft-state filter.
	if err := transition(t, s, taskTestNow, task1, "draft", "ready"); err != nil {
		t.Fatalf("transition task1 to ready: %v", err)
	}
	if err := transition(t, s, taskTestNow, task2, "draft", "ready"); err != nil {
		t.Fatalf("transition task2 to ready: %v", err)
	}

	if !isBlocked(t, s, task2) {
		t.Fatalf("IsBlocked(%s): want true while task1 open", task2)
	}
	ready, err := s.readyCandidates(t.Context(), "p1", "")
	if err != nil {
		t.Fatalf("readyCandidates: %v", err)
	}
	for _, r := range ready {
		if r.ID == task2 {
			t.Fatalf("readyCandidates offered %s, which task1 still blocks", task2)
		}
	}

	walkTo(t, s, task1, "merged")

	if isBlocked(t, s, task2) {
		t.Fatalf("IsBlocked(%s): want false after task1 merged", task2)
	}
	ready, err = s.readyCandidates(t.Context(), "p1", "")
	if err != nil {
		t.Fatalf("readyCandidates after release: %v", err)
	}
	found := false
	for _, r := range ready {
		if r.ID == task2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("readyCandidates after task1 merged omitted %s", task2)
	}
}

// planMintBodyFourth is planMintBody with a fourth declaration appended,
// blocked by task 1, and Task 2's prose rewritten: the shape of a plan edited
// after acceptance (025 §9.2).
var planMintBodyFourth = strings.Replace(planMintBody,
	"Do the second thing.", "Do the second thing, differently.", 1) + `
### Task 4 — Fourth task

` + "```yaml" + `
kind: chore
priority: high
blockedBy: [1]
` + "```" + `

Do the fourth thing.
`

// taskSnapshot is the part of a minted task a re-accept must not touch.
type taskSnapshot struct {
	Title, Body, Kind, Priority, State string
	UpdatedAt                          time.Time
}

func snapshotTask(t *testing.T, s *Store, id string) taskSnapshot {
	t.Helper()
	task, err := s.GetTask(t.Context(), id)
	if err != nil {
		t.Fatalf("GetTask(%s): %v", id, err)
	}
	return taskSnapshot{
		Title: task.Title, Body: task.Body, Kind: task.Kind,
		Priority: task.Priority, State: task.State, UpdatedAt: task.UpdatedAt,
	}
}

// TestDocAcceptPlanReAcceptMintsOnlyNewDeclarations: an accepted plan stays
// freely mutable, so re-accepting one mints the declarations that have no row
// yet and leaves every existing row alone (025 §9.2) — including a row whose
// declaration's prose changed, and one that has since left draft. The new
// task's declared blockedBy is wired even though its blocker was minted by the
// first accept.
func TestDocAcceptPlanReAcceptMintsOnlyNewDeclarations(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "remint-plan", Body: planMintBody, CreatedBy: "stig",
	})

	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	if len(minted) != 3 {
		t.Fatalf("first accept minted %d tasks, want 3", len(minted))
	}
	// Execution has started on task 1: a re-accept must not walk it back.
	if err := transition(t, s, taskTestNow, minted[0].ID, "draft", "ready"); err != nil {
		t.Fatalf("transition %s to ready: %v", minted[0].ID, err)
	}
	before := map[string]taskSnapshot{}
	for _, task := range minted {
		before[task.ID] = snapshotTask(t, s, task.ID)
	}

	if _, err := updateDocBody(t, s, doc.ID, planMintBodyFourth); err != nil {
		t.Fatalf("UpdateDocBody on the accepted plan: %v", err)
	}

	accepted, again, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("re-accept: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Errorf("status = %q, want accepted", accepted.Status)
	}
	if len(again) != 1 {
		t.Fatalf("re-accept minted %d tasks, want 1", len(again))
	}
	if again[0].Title != "Fourth task" || again[0].Kind != "chore" || again[0].Priority != "high" {
		t.Errorf("minted %+v, want the fourth declaration", again[0])
	}
	if got := countTasksWithPlanDoc(t, s, doc.ID); got != 4 {
		t.Errorf("tasks with plan_doc = %d, want 4", got)
	}

	for id, want := range before {
		if got := snapshotTask(t, s, id); got != want {
			t.Errorf("task %s = %+v after the re-accept, want it untouched (%+v)", id, got, want)
		}
	}

	// The new task's blockedBy names a task the first accept minted, so the
	// edge crosses the two accepts.
	var edgeType string
	err = s.db.QueryRow(`SELECT type FROM task_edges WHERE from_task = $1 AND to_task = $2`,
		minted[0].ID, again[0].ID).Scan(&edgeType)
	if err != nil {
		t.Fatalf("read edge %s -> %s: %v", minted[0].ID, again[0].ID, err)
	}
	if edgeType != "blocks" {
		t.Errorf("edge type = %q, want blocks", edgeType)
	}
}

// TestDocAcceptPlanReAcceptWithoutNewDeclarationsIsNoOp: an edit that adds no
// declaration — prose rewritten under an existing one — leaves the re-accept
// with nothing to mint, and that is a success rather than an error (025 §9.2).
func TestDocAcceptPlanReAcceptWithoutNewDeclarationsIsNoOp(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "noop-plan", Body: planMintBody, CreatedBy: "stig",
	})

	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	before := map[string]taskSnapshot{}
	for _, task := range minted {
		before[task.ID] = snapshotTask(t, s, task.ID)
	}

	edited := strings.Replace(planMintBody, "Do the first thing.", "Do the first thing, carefully.", 1)
	if _, err := updateDocBody(t, s, doc.ID, edited); err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}

	_, again, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("re-accept: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("re-accept minted %d tasks, want none", len(again))
	}
	if got := countTasksWithPlanDoc(t, s, doc.ID); got != len(minted) {
		t.Errorf("tasks with plan_doc = %d, want unchanged %d", got, len(minted))
	}
	for id, want := range before {
		if got := snapshotTask(t, s, id); got != want {
			t.Errorf("task %s = %+v after the no-op re-accept, want it untouched (%+v)", id, got, want)
		}
	}
}

// TestDocAcceptPlanReAcceptNeverRemintsDeletedTask: a soft-deleted task keeps
// its declaration's key, so the withdrawal survives the next re-accept
// (025 §9.2 — withdrawing work is a task transition, and re-acceptance leaves
// existing rows alone).
func TestDocAcceptPlanReAcceptNeverRemintsDeletedTask(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "withdrawn-plan", Body: planMintBody, CreatedBy: "stig",
	})

	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	if err := deleteTask(t, s, minted[2].ID, "stig", "not needed after all"); err != nil {
		t.Fatalf("delete %s: %v", minted[2].ID, err)
	}

	_, again, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("re-accept: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("re-accept minted %d tasks, want none: the declaration still has its row", len(again))
	}
}

// TestDocUpdateBodyMintedPlanRequiresReadableTasks: once a plan has minted
// tasks, a body edit that leaves its ## Tasks section unreadable is refused at
// the write rather than surfacing as drift at the next accept (025 §9.2). The
// body does not move.
func TestDocUpdateBodyMintedPlanRequiresReadableTasks(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "drift-plan", Body: planMintBody, CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}

	for name, body := range map[string]string{
		"section removed":  "---\nstatus: accepted\n---\n\n# A plan\n\nNo tasks here.\n",
		"kindless fence":   strings.Replace(planMintBody, "kind: feature\n", "", 1),
		"duplicate titles": strings.Replace(planMintBody, "Task 3 — Third task", "Task 3 — First task", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := updateDocBody(t, s, doc.ID, body)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			got, err := s.GetDoc(t.Context(), doc.ID)
			if err != nil {
				t.Fatalf("GetDoc: %v", err)
			}
			if got.Body != planMintBody {
				t.Errorf("body moved to %q, want the refused edit not to land", got.Body)
			}
		})
	}
}

// TestDocUpdateBodyUnmintedPlanTasksUnchecked: the readable-## Tasks rule
// binds only once something is minted. A draft plan is written a paragraph at
// a time, and an accepted plan that minted nothing is §9.2's historical
// import; neither has a task set to drift from.
func TestDocUpdateBodyUnmintedPlanTasksUnchecked(t *testing.T) {
	s := openDocStore(t)
	draft := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "draft-plan", Body: planMintBody, CreatedBy: "stig",
	})
	half := "---\nstatus: draft\n---\n\n# A plan\n\nStill thinking.\n"
	if _, err := updateDocBody(t, s, draft.ID, half); err != nil {
		t.Fatalf("UpdateDocBody on an unaccepted plan: %v", err)
	}

	imported := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "imported-plan", Body: planBody,
		CreatedBy: "stig", Status: "accepted",
	})
	if _, err := updateDocBody(t, s, imported.ID, half); err != nil {
		t.Fatalf("UpdateDocBody on an accepted plan that minted nothing: %v", err)
	}
}

// TestDocUpdateBodyPlanBumpsVersion: a plan is edited in place rather than
// revised (025 §9), so its body edit is the next version of the document —
// which is what lets the acceptance event of §15.3, keyed on IRI and version,
// tell a re-accept after an edit from a retry of the same accept.
func TestDocUpdateBodyPlanBumpsVersion(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "versioned-plan", Body: planMintBody, CreatedBy: "stig",
	})
	if doc.Version != 1 {
		t.Fatalf("version = %d, want 1", doc.Version)
	}
	updated, err := updateDocBody(t, s, doc.ID,
		strings.Replace(planMintBody, "Do the first thing.", "Do it now.", 1))
	if err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("version = %d, want 2", updated.Version)
	}

	// A draft spec's body edit is not a publication, so its version holds.
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 91, Slug: "091-x", Body: specBody, CreatedBy: "stig",
	})
	editedSpec, err := updateDocBody(t, s, spec.ID, specBody+"\nmore\n")
	if err != nil {
		t.Fatalf("UpdateDocBody on a draft spec: %v", err)
	}
	if editedSpec.Version != 1 {
		t.Errorf("spec version = %d, want 1", editedSpec.Version)
	}
}

// TestPlanTaskKeyBackfillDisambiguatesDuplicateTitles: 0043 backfills
// plan_task_key from tasks.title, and nothing before it stopped two
// declarations in one plan from sharing a title. The earliest row keeps the
// title — the key a re-accept then matches that declaration to — and the rest
// are disambiguated, so the partial unique index can be created and no
// existing row is lost.
func TestPlanTaskKeyBackfillDisambiguatesDuplicateTitles(t *testing.T) {
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 42)); err != nil {
		t.Fatalf("migrate through 0042: %v", err)
	}
	db := s.DBForTests()
	if _, err := db.Exec(`INSERT INTO projects (id, name, key) VALUES ('p1','P1','P1')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var planID int64
	if err := db.QueryRow(
		`INSERT INTO docs (project_id, kind, slug, title, body, created_at, updated_at)
		 VALUES ('p1', 'plan', 'dup-plan', 'Dup plan', 'body', now(), now()) RETURNING id`,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan doc: %v", err)
	}
	// Inserted directly: the store is held at 0042, where tasks has no
	// plan_task_key column for CreateTask to write.
	for i, id := range []string{"P1-1", "P1-2", "P1-3"} {
		created := taskTestNow.Add(time.Duration(i) * time.Second)
		title := "Same title"
		if id == "P1-3" {
			title = "Its own title"
		}
		if _, err := db.Exec(
			`INSERT INTO tasks (id, project_id, title, body, priority, kind, state,
			                    created_at, updated_at, plan_doc)
			 VALUES ($1, 'p1', $2, 'b', 'medium', 'feature', 'draft', $3, $3, $4)`,
			id, title, created, planID); err != nil {
			t.Fatalf("seed task %s: %v", id, err)
		}
	}

	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	keys := map[string]string{}
	rows, err := db.Query(`SELECT id, plan_task_key FROM tasks ORDER BY id`)
	if err != nil {
		t.Fatalf("read keys: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			t.Fatalf("scan key: %v", err)
		}
		keys[id] = key
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read keys: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("kept %d tasks, want all 3", len(keys))
	}
	if keys["P1-1"] != "Same title" {
		t.Errorf("earliest duplicate key = %q, want the title itself", keys["P1-1"])
	}
	if keys["P1-2"] == "Same title" || keys["P1-2"] == "" {
		t.Errorf("later duplicate key = %q, want it disambiguated", keys["P1-2"])
	}
	if keys["P1-3"] != "Its own title" {
		t.Errorf("unique title key = %q, want the title itself", keys["P1-3"])
	}
}

// TestDocAcceptPlanParseFailureRefusesAndStaysDraft: a plan whose body fails
// designdoc.PlanTasks refuses to accept, wrapped as ErrInvalidInput, and its
// status stays draft — the status flip and the mint never run.
func TestDocAcceptPlanParseFailureRefusesAndStaysDraft(t *testing.T) {
	frontmatter := "---\nstatus: draft\n---\n\n# A plan\n\n"
	cases := map[string]string{
		"no tasks section": frontmatter + "No tasks here.\n",
		"dangling blockedBy": frontmatter + `## Tasks

### Task 1 — Only task

` + "```yaml" + `
kind: feature
blockedBy: [2]
` + "```" + `

Do it.
`,
		"cyclic blockedBy": frontmatter + `## Tasks

### Task 1 — First

` + "```yaml" + `
kind: feature
blockedBy: [2]
` + "```" + `

a

### Task 2 — Second

` + "```yaml" + `
kind: feature
blockedBy: [1]
` + "```" + `

b
`,
		"missing kind": frontmatter + `## Tasks

### Task 1 — Only task

` + "```yaml" + `
priority: medium
` + "```" + `

Do it.
`,
		"unmintable kind": frontmatter + `## Tasks

### Task 1 — Only task

` + "```yaml" + `
kind: review
` + "```" + `

Do it.
`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s := openDocStore(t)
			doc := mustCreateDoc(t, s, DocInput{
				Project: "p1", Kind: "plan", Slug: "bad-plan", Body: body, CreatedBy: "stig",
			})

			_, minted, err := acceptDoc(t, s, doc.ID, "stig")
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if minted != nil {
				t.Errorf("minted = %v, want nil", minted)
			}
			got, err := s.GetDoc(t.Context(), doc.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "draft" {
				t.Errorf("status = %q, want draft", got.Status)
			}
			if n := countTasksWithPlanDoc(t, s, doc.ID); n != 0 {
				t.Errorf("tasks with plan_doc after refused accept = %d, want 0", n)
			}
		})
	}
}

// TestDocAcceptPlanWrongActorForbidden: acceptance is the assignee's act,
// exactly as for a spec (025 §7); a forbidden accept mints nothing.
func TestDocAcceptPlanWrongActorForbidden(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "gated-plan", Body: planMintBody, CreatedBy: "stig",
	})

	if _, _, err := acceptDoc(t, s, doc.ID, "ada"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if got, err := s.GetDoc(t.Context(), doc.ID); err != nil || got.Status != "draft" {
		t.Fatalf("doc = %+v, %v; want it still draft", got, err)
	}
	if n := countTasksWithPlanDoc(t, s, doc.ID); n != 0 {
		t.Errorf("tasks with plan_doc after forbidden accept = %d, want 0", n)
	}
}

// TestDocPlanTasksMintedMetric: RecordPlanTasksMinted adds n to
// worklode_doc_plan_tasks_minted_total. AcceptDoc itself does not call it —
// it is a package-level function with no *Store — so the API handler calls
// it after the accepting transaction commits; this exercises that method
// directly, the pattern TestDocOperationsMetric above uses for docOp.
func TestDocPlanTasksMintedMetric(t *testing.T) {
	s := openDocStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)

	s.RecordPlanTasksMinted(3)
	if got := testutil.ToFloat64(s.metrics.docTasksMinted); got != 3 {
		t.Fatalf("worklode_doc_plan_tasks_minted_total = %v, want 3", got)
	}
	s.RecordPlanTasksMinted(2)
	if got := testutil.ToFloat64(s.metrics.docTasksMinted); got != 5 {
		t.Fatalf("worklode_doc_plan_tasks_minted_total = %v, want 5", got)
	}
}

// TestDocPlanTasksMintedMetricNilSafe: a store opened without WithMetrics
// records nothing.
func TestDocPlanTasksMintedMetricNilSafe(t *testing.T) {
	var m *storeMetrics
	m.planTasksMinted(3)
}

// TestRecordDocOpMetric: RecordDocOp is the exported way into
// worklode_doc_operations_total for the handlers that record their event
// through eventbus.Emit instead of RecordDocEvent (025 §15.3), so accept and
// submit stay counted with every other document verb.
func TestRecordDocOpMetric(t *testing.T) {
	s := openDocStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)

	s.RecordDocOp("submit", nil)
	s.RecordDocOp("accept", errors.New("boom"))
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("submit", "ok")); got != 1 {
		t.Errorf(`docOps{op=submit,outcome=ok} = %v, want 1`, got)
	}
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("accept", "error")); got != 1 {
		t.Errorf(`docOps{op=accept,outcome=error} = %v, want 1`, got)
	}
}

// TestDocAcceptSupersedesReplacedDoc: a document-level replaces edge flips an
// accepted target in the same transaction (025 §3.3), and leaves a draft one
// alone — 025 §7's ladder runs draft -> accepted -> superseded, and a draft
// jumped straight to superseded would be reachable by no verb here.
func TestDocAcceptSupersedesReplacedDoc(t *testing.T) {
	s := openDocStore(t)
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	unaccepted := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 7, Slug: "007-draft", Body: specBody,
		CreatedBy: "stig",
	})
	body := "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md\n    - 007-draft.md\n---\n\n" +
		"# New\n\n## 1. Scope {#sec-1}\n\na\n"
	newDoc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", Body: body, CreatedBy: "stig",
	})

	if _, _, err := acceptDoc(t, s, newDoc.ID, "stig"); err != nil {
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

	stillDraft, err := s.GetDoc(t.Context(), unaccepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillDraft.Status != "draft" {
		t.Errorf("draft target status = %q, want it left as draft", stillDraft.Status)
	}
	// Nothing moved, so nothing is logged.
	entries, err = s.StateLogForEntity(t.Context(), "doc", strconv.FormatInt(unaccepted.ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("state log = %+v, want only the create entry", entries)
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

	if _, _, err := acceptDoc(t, s, newDoc.ID, "stig"); err != nil {
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

	_, _, err := acceptDoc(t, s, doc.ID, "stig")
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

	if _, _, err := acceptDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
}

// TestDocAcceptNotFound covers the unknown-id path.
func TestDocAcceptNotFound(t *testing.T) {
	s := openDocStore(t)
	if _, _, err := acceptDoc(t, s, 9999, "stig"); !errors.Is(err, ErrNotFound) {
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

// TestDocDiscardRevisionStanding: the assignee and the revision's author may
// each withdraw an open candidate; a third party may not (025 §7.2).
//
// mustAcceptedSpec's documents are created by stig, and CreateDoc defaults the
// assignee to the creator, so stig is the assignee throughout and ada is the
// proposer.
func TestDocDiscardRevisionStanding(t *testing.T) {
	t.Run("author", func(t *testing.T) {
		s := openDocStore(t)
		doc := mustAcceptedSpec(t, s, "025-x")
		if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
			t.Fatalf("ReviseDoc: %v", err)
		}
		if _, err := discardRevision(t, s, doc.ID, "ada"); err != nil {
			t.Fatalf("DiscardRevision by the author: %v", err)
		}
		if _, err := s.GetDocRevision(t.Context(), doc.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetDocRevision after discard = %v, want ErrNotFound", err)
		}
	})

	t.Run("assignee", func(t *testing.T) {
		s := openDocStore(t)
		doc := mustAcceptedSpec(t, s, "025-x")
		if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
			t.Fatalf("ReviseDoc: %v", err)
		}
		if _, err := discardRevision(t, s, doc.ID, "stig"); err != nil {
			t.Fatalf("DiscardRevision by the assignee: %v", err)
		}
		if _, err := s.GetDocRevision(t.Context(), doc.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetDocRevision after discard = %v, want ErrNotFound", err)
		}
	})

	t.Run("third party", func(t *testing.T) {
		s := openDocStore(t)
		doc := mustAcceptedSpec(t, s, "025-x")
		if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
			t.Fatalf("ReviseDoc: %v", err)
		}
		if _, err := discardRevision(t, s, doc.ID, "bob"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("DiscardRevision by a third party = %v, want ErrForbidden", err)
		}
		if _, err := s.GetDocRevision(t.Context(), doc.ID); err != nil {
			t.Fatalf("the refused discard removed the candidate anyway: %v", err)
		}
	})
}

// TestDocDiscardRevisionFreesTheSlot: doc_revisions is keyed on doc_id, so the
// point of a discard is that the next ReviseDoc succeeds immediately rather
// than hitting ErrRevisionExists. The accepted version is untouched by either.
func TestDocDiscardRevisionFreesTheSlot(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	back, err := discardRevision(t, s, doc.ID, "ada")
	if err != nil {
		t.Fatalf("DiscardRevision: %v", err)
	}
	if back.Version != 1 || back.Body != specBody {
		t.Errorf("returned doc = version %d, want the accepted version untouched", back.Version)
	}

	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc after a discard: %v, want the slot free", err)
	}
	rev, err := s.GetDocRevision(t.Context(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.CreatedBy != "stig" || rev.Body != specBody {
		t.Errorf("revision = %+v, want a fresh copy of the accepted body opened by stig", rev)
	}
	got, err := s.GetDoc(t.Context(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Body != specBody {
		t.Errorf("doc = version %d, want the accepted version untouched by a discard", got.Version)
	}
}

// TestDocDiscardRevisionWithoutOpenRevision: nothing to withdraw is
// ErrNotFound, for the assignee as much as for anyone.
func TestDocDiscardRevisionWithoutOpenRevision(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")

	if _, err := discardRevision(t, s, doc.ID, "stig"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// setDocStatus forces a document's status, standing in for the states only
// another act — a supersession, a corpus import — can produce.
func setDocStatus(t *testing.T, s *Store, id int64, status string) {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE docs SET status = $2 WHERE id = $1`, id, status); err != nil {
		t.Fatal(err)
	}
}

// TestDocUpdateRevisionOnSupersededDoc: a document superseded since the
// revision opened has nothing left to land, and says so at the edit rather
// than at the accept gate.
func TestDocUpdateRevisionOnSupersededDoc(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	setDocStatus(t, s, doc.ID, "superseded")

	err := updateRevision(t, s, doc.ID, revisedSpecBody)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "superseded") {
		t.Errorf("err = %v, want it to name the status", err)
	}
}

// TestDocDiscardRevisionOnSupersededDoc: the case that separates discard from
// the other two revision verbs. A candidate stranded on a document superseded
// since it opened can no longer be edited or landed, so withdrawing it must
// still work — otherwise the row is unremovable.
func TestDocDiscardRevisionOnSupersededDoc(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	setDocStatus(t, s, doc.ID, "superseded")

	if _, err := discardRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("DiscardRevision on a superseded doc: %v", err)
	}
	if _, err := s.GetDocRevision(t.Context(), doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDocRevision after discard = %v, want ErrNotFound", err)
	}
}

// TestDocDiscardRevisionLogsTheWithdrawnBody: doc_revisions has no history and
// the discard is a hard delete, so the state_log row is the only surviving
// copy of the withdrawn text.
func TestDocDiscardRevisionLogsTheWithdrawnBody(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	if _, err := discardRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("DiscardRevision: %v", err)
	}

	var change []byte
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT change FROM state_log
		  WHERE entity_kind = 'doc' AND entity_id = $1
		    AND change->>'new' = 'discarded'
		  ORDER BY id DESC LIMIT 1`, strconv.FormatInt(doc.ID, 10)).Scan(&change); err != nil {
		t.Fatalf("read the discard's state_log row: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(change, &got); err != nil {
		t.Fatal(err)
	}
	if got["discarded_body"] != revisedSpecBody {
		t.Errorf("discarded_body = %q, want the withdrawn candidate", got["discarded_body"])
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
// section is never renumbered (025 §6 rule 3). Renumbering while keeping the
// anchor — "## 3. … {#sec-2}" — is a lintAnchors defect and never reaches the
// diff, so the renumber arrives here the other way: the anchor moves with the
// number and sec-2 reads as removed. Its twin below covers the form that does
// reach rule 3.
func TestDocAcceptRevisionRejectsRenumber(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
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

// TestDocAcceptRevisionRejectsDroppedNumber: dropping a section's number while
// keeping its anchor passes lintAnchors — which only compares a number it has
// — and so reaches rule 3 as an actual renumber, "2" to "".
func TestDocAcceptRevisionRejectsDroppedNumber(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	unnumbered := strings.Replace(specBody, "## 2. Model {#sec-2}", "## Model {#sec-2}", 1)
	if err := updateRevision(t, s, doc.ID, unnumbered); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	_, err := acceptRevision(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), `sec-2: renumbered from "2" to ""`) {
		t.Errorf("err = %v, want the rule 3 violation naming both numbers", err)
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

// TestDocAcceptRevisionStampsEveryChangedSection: last_revised_in moves on
// each of several changed anchors and on none of the untouched ones — the
// stamp is one UPDATE over the anchor set, so a whole-document or
// first-anchor-only stamp would show here and not in the single-change case.
// It also checks the rebuilt rows' own columns, since they are written from
// parallel arrays where a transposition would silently swap headings.
func TestDocAcceptRevisionStampsEveryChangedSection(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	// sec-2 and sec-2.1 both get new bodies; sec-1 is left verbatim.
	revised := strings.NewReplacer(
		"status: draft", "status: accepted",
		"Model body.", "Model body, revised.",
		"Detail body.", "Detail body, revised.",
	).Replace(specBody)
	if err := updateRevision(t, s, doc.ID, revised); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	if _, err := acceptRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}

	want := []model.DocSection{
		{Anchor: "sec-1", Number: "1", Heading: "Scope", Depth: 2, Position: 0, LastRevisedIn: 1, Published: true},
		{Anchor: "sec-2", Number: "2", Heading: "Model", Depth: 2, Position: 1, LastRevisedIn: 2, Published: true},
		{Anchor: "sec-2.1", Number: "2.1", Heading: "Detail", Depth: 3, Position: 2, LastRevisedIn: 2, Published: true},
	}
	if got := docSections(t, s, doc.ID); !slices.Equal(got, want) {
		t.Errorf("sections =\n%+v\nwant\n%+v", got, want)
	}
}

// subheadingSpecBody is a spec whose sec-2 holds an anchorless "#### Tie-
// breaking" block — legal per 025 §6.1, which makes a heading deeper than the
// addressability limit content within its nearest anchored ancestor rather
// than a node of its own.
const subheadingSpecBody = `---
status: draft
issued: 2026-08-01
---

# Documents in the backbone

Intro prose.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.

#### Tie-breaking

Oldest first.
`

// TestDocAcceptRevisionStampsAnchorlessSubheadingEdit: an edit confined to an
// anchorless subheading moves its anchored ancestor's last_revised_in in the
// database. Section.Body stops at the next heading of any level, so a diff
// over bodies alone would accept this revision as touching nothing and leave
// every coverage claim against sec-2 falsely fresh — the silent-staleness half
// of 025 §6 rule 5.
func TestDocAcceptRevisionStampsAnchorlessSubheadingEdit(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-subheading",
		Body: subheadingSpecBody, CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	revised := strings.NewReplacer(
		"status: draft", "status: accepted",
		"Oldest first.", "Highest priority first.",
	).Replace(subheadingSpecBody)
	if err := updateRevision(t, s, doc.ID, revised); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	if _, err := acceptRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}

	want := []model.DocSection{
		{Anchor: "sec-1", Number: "1", Heading: "Scope", Depth: 2, Position: 0, LastRevisedIn: 1, Published: true},
		{Anchor: "sec-2", Number: "2", Heading: "Model", Depth: 2, Position: 1, LastRevisedIn: 2, Published: true},
	}
	if got := docSections(t, s, doc.ID); !slices.Equal(got, want) {
		t.Errorf("sections =\n%+v\nwant\n%+v", got, want)
	}
}

// TestDocAcceptSupersedesEveryReplacedDoc: a document replacing several
// accepted documents flips and logs all of them, not just the first — the
// flip is one UPDATE ... RETURNING over the target set.
func TestDocAcceptSupersedesEveryReplacedDoc(t *testing.T) {
	s := openDocStore(t)
	var replaced []int64
	for _, spec := range []struct {
		number int
		slug   string
	}{{6, "006-old"}, {7, "007-older"}, {8, "008-oldest"}} {
		d := mustCreateDoc(t, s, DocInput{
			Project: "p1", Kind: "spec", Number: spec.number, Slug: spec.slug, Body: specBody,
			CreatedBy: "stig", Status: "accepted",
		})
		replaced = append(replaced, d.ID)
	}
	body := "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md\n    - 007-older.md\n" +
		"    - 008-oldest.md\n---\n\n# New\n\n## 1. Scope {#sec-1}\n\na\n"
	newDoc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", Body: body, CreatedBy: "stig",
	})

	if _, _, err := acceptDoc(t, s, newDoc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	for _, id := range replaced {
		got, err := s.GetDoc(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "superseded" {
			t.Errorf("doc %d status = %q, want superseded", id, got.Status)
		}
		entries, err := s.StateLogForEntity(t.Context(), "doc", strconv.FormatInt(id, 10))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 || !strings.Contains(entries[1].Change, `"superseded"`) {
			t.Errorf("doc %d state log = %+v, want a superseded entry", id, entries)
		}
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

// TestDocAcceptRevisionWrongStatus: only an accepted document has a revision
// to land. Both other statuses are refused, whatever left a candidate row
// behind.
func TestDocAcceptRevisionWrongStatus(t *testing.T) {
	for _, status := range []string{"draft", "superseded"} {
		t.Run(status, func(t *testing.T) {
			s := openDocStore(t)
			doc := mustAcceptedSpec(t, s, "025-x")
			if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
				t.Fatalf("ReviseDoc: %v", err)
			}
			setDocStatus(t, s, doc.ID, status)

			_, err := acceptRevision(t, s, doc.ID, "stig")
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), status) {
				t.Errorf("err = %v, want it to name the status", err)
			}
		})
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

// TestDocListSections: the reader the detail endpoint serves returns a spec's
// sections in document order, and nothing at all for a plan (025 §9).
func TestDocListSections(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan",
		Slug: "025-documents-in-the-backbone-2", Body: planBody, CreatedBy: "stig",
	})

	got, err := s.ListDocSections(t.Context(), spec.ID)
	if err != nil {
		t.Fatalf("ListDocSections: %v", err)
	}
	want := []model.DocSection{
		{Anchor: "sec-1", Number: "1", Heading: "Scope", Depth: 2, Position: 0, LastRevisedIn: 1},
		{Anchor: "sec-2", Number: "2", Heading: "Model", Depth: 2, Position: 1, LastRevisedIn: 1},
		{Anchor: "sec-2.1", Number: "2.1", Heading: "Detail", Depth: 3, Position: 2, LastRevisedIn: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("sections = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("section %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	if got, err := s.ListDocSections(t.Context(), plan.ID); err != nil || len(got) != 0 {
		t.Fatalf("plan sections = %+v, %v; want none", got, err)
	}
}

// TestDocListEdgesBothDirections: the same row is read forward out of the
// document that declared it and backward into the document it names, where it
// carries its inverse spelling and points back at the other end (025 §14).
// Every resolved far end also carries the other document's project, slug, kind
// and number, so a reader can name it; an unresolved reference carries none.
func TestDocListEdgesBothDirections(t *testing.T) {
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
		if out[i] != wantOut[i] {
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
	if len(out) != 1 || out[0] != (model.DocEdge{Type: "requires", ToExternal: "004-execution-backbone.md#sec-6"}) {
		t.Fatalf("spec edges out = %+v, want one external requires", out)
	}
	// The covers edge lands on the spec's #sec-5, so from the spec's end that
	// is the near anchor and the plan is the far end.
	planFar := func(e model.DocEdge) model.DocEdge {
		// A plan carries no corpus number (025 §14.3), so its far end names
		// its kind and slug alone.
		e.ToDoc, e.ToProject, e.ToSlug, e.ToKind = plan.ID, "p1", plan.Slug, "plan"
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
		if in[i] != wantIn[i] {
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
	if !slices.Equal(out, want) {
		t.Fatalf("edges out of the near plan = %+v, want %+v", out, want)
	}

	_, in, err := s.ListDocEdges(t.Context(), far.ID)
	if err != nil {
		t.Fatalf("ListDocEdges(far): %v", err)
	}
	wantIn := []model.DocEdge{{
		Type: "hadDerivation", ToDoc: near.ID,
		ToProject: "p1", ToSlug: "plan-across", ToKind: "plan",
	}}
	if !slices.Equal(in, wantIn) {
		t.Fatalf("edges into the far spec = %+v, want %+v", in, wantIn)
	}
}

// TestDocListEdgesInverseCoversEveryType: every type the doc_edges CHECK
// admits must have an inverse, or reading a document's inbound edges states
// the relation backwards. One edge of each type, read from the far end.
func TestDocListEdgesInverseCoversEveryType(t *testing.T) {
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
	if !slices.Equal(got, want) {
		t.Fatalf("edges of plan-two = %+v, want %+v", got, want)
	}
	// The blockedBy row leaves plan-one, which is where "plan-one blocks
	// plan-two" belongs — plan-two only authored it.
	gotOne := docEdges(t, s, one.ID)
	wantOne := []model.DocEdge{{Type: "blocks", ToDoc: two.ID}}
	if !slices.Equal(gotOne, wantOne) {
		t.Fatalf("edges of plan-one = %+v, want %+v", gotOne, wantOne)
	}
}

// TestDocBlockedByWritesTheSameRowAsBlocks: the whole point of the inverse
// spelling is authoring order. A numbered plan series is written forward, so
// part 3 must be able to say it follows part 2 without part 2 being edited —
// and the row it writes is byte-for-byte the one part 2's `blocks:` would
// have written (025 §5, WL-143).
func TestDocBlockedByWritesTheSameRowAsBlocks(t *testing.T) {
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
	if !slices.Equal(got, want) {
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
	if got, want := docEdges(t, s, a.ID), []model.DocEdge{{Type: "blocks", ToDoc: b.ID}}; !slices.Equal(got, want) {
		t.Fatalf("edges of plan-second-early declared with blocks = %+v, want %+v", got, want)
	}
}

// TestDocBlockedByIsOwnedByItsAuthor: the row leaves the *other* plan, so the
// rewrite that clears it has to be scoped by who declared it (doc_edges
// .declared_by), not by where it points from. Dropping the key drops the row;
// rewriting the blocking plan does not.
func TestDocBlockedByIsOwnedByItsAuthor(t *testing.T) {
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
	if got := docEdges(t, s, early.ID); !slices.Equal(got, want) {
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
	if got := docEdges(t, s, early.ID); !slices.Equal(got, want) {
		t.Fatalf("edges of plan-early = %+v, want exactly %+v", got, want)
	}
}

// TestDocEdgesRejectBadBlockedByEnds: `blockedBy` is the same edge, so it is
// held to the same guards — both ends plans, the reference resolvable, no
// self-block, no cycle. Reading them off the row rather than off the author is
// what keeps the two spellings from disagreeing about what is legal.
func TestDocEdgesRejectBadBlockedByEnds(t *testing.T) {
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

// blockingPlanBody renders a draft plan ordering itself before blocked.
func blockingPlanBody(blocked string) string {
	return "---\nstatus: draft\nblocks: " + blocked + "\n---\n\n# Plan\n"
}

// TestDocEdgesRejectBlocksCycleBetweenPlans: a cycle through two or more plans
// wedges every plan in it — each plan's tasks are held by the next plan's open
// set, so no set can ever close and Claim answers ErrBlocked forever. Plans
// stay mutable at any status, so it is the write that closes the cycle that
// has to refuse it, and the refusal names the cycle (WL-144).
func TestDocEdgesRejectBlocksCycleBetweenPlans(t *testing.T) {
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
	if !slices.Equal(got, want) {
		t.Fatalf("edges of plan-first = %+v, want %+v", got, want)
	}
}

// --- NeedsPlanning / NeedsExecution (026 §2) -----------------------------

// planCoveringBody renders a plan whose frontmatter covers refs and whose
// ## Tasks section holds one definition, so the plan can be accepted.
func planCoveringBody(refs ...string) string {
	var b strings.Builder
	b.WriteString("---\nstatus: draft\n")
	if len(refs) > 0 {
		b.WriteString("covers:\n")
		for _, ref := range refs {
			b.WriteString("  - " + ref + "\n")
		}
	}
	b.WriteString("---\n\n# A covering plan\n\n## Tasks\n\n### Task 1 — Only task\n\n")
	b.WriteString("```yaml\nkind: chore\n```\n\nDo it.\n")
	return b.String()
}

// coveringPlan creates a plan covering refs, accepting it when accept is set.
func coveringPlan(t *testing.T, s *Store, slug string, accept bool, refs ...string) *model.Doc {
	t.Helper()
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: slug, Body: planCoveringBody(refs...), CreatedBy: "stig",
	})
	if !accept {
		return doc
	}
	accepted, _, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("accept plan %s: %v", slug, err)
	}
	return accepted
}

// coverageRef is one `covers` entry for a levelled test plan body: a target
// reference, its authored level ("" renders the bare-string full-coverage
// form), and — for a partial entry — the fullCoverageWith closure.
type coverageRef struct {
	ref              string
	level            string
	fullCoverageWith []string
}

// levelledPlanBody renders a plan whose frontmatter covers refs with
// explicit coverage levels and fullCoverageWith closures (026 §2.1, §5), and
// whose ## Tasks section holds one definition so the plan can be accepted.
func levelledPlanBody(refs ...coverageRef) string {
	var b strings.Builder
	b.WriteString("---\nstatus: draft\n")
	if len(refs) > 0 {
		b.WriteString("covers:\n")
		for _, r := range refs {
			if r.level == "" && len(r.fullCoverageWith) == 0 {
				b.WriteString("  - " + r.ref + "\n")
				continue
			}
			b.WriteString("  - spec: " + r.ref + "\n")
			if r.level != "" {
				b.WriteString("    coverage: " + r.level + "\n")
			}
			if len(r.fullCoverageWith) > 0 {
				b.WriteString("    fullCoverageWith:\n")
				for _, cw := range r.fullCoverageWith {
					b.WriteString("      - " + cw + "\n")
				}
			}
		}
	}
	b.WriteString("---\n\n# A covering plan\n\n## Tasks\n\n### Task 1 — Only task\n\n")
	b.WriteString("```yaml\nkind: chore\n```\n\nDo it.\n")
	return b.String()
}

// levelledPlan creates a plan covering refs at explicit levels, accepting it
// when accept is set.
func levelledPlan(t *testing.T, s *Store, slug string, accept bool, refs ...coverageRef) *model.Doc {
	t.Helper()
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: slug, Body: levelledPlanBody(refs...), CreatedBy: "stig",
	})
	if !accept {
		return doc
	}
	accepted, _, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("accept plan %s: %v", slug, err)
	}
	return accepted
}

// deferralRef is one `defers` entry for a test plan body: the deferred
// section and its named owner (026 §5.3).
type deferralRef struct {
	spec string
	to   string
}

// deferringPlanBody renders a mintable plan whose frontmatter defers each ref
// to its named owner (026 §5.3), and optionally covers others at explicit
// levels alongside it — the NeedsPlanning precedence tests need both keys on
// one plan.
func deferringPlanBody(defers []deferralRef, covers ...coverageRef) string {
	var b strings.Builder
	b.WriteString("---\nstatus: draft\n")
	if len(covers) > 0 {
		b.WriteString("covers:\n")
		for _, r := range covers {
			if r.level == "" && len(r.fullCoverageWith) == 0 {
				b.WriteString("  - " + r.ref + "\n")
				continue
			}
			b.WriteString("  - spec: " + r.ref + "\n")
			if r.level != "" {
				b.WriteString("    coverage: " + r.level + "\n")
			}
			if len(r.fullCoverageWith) > 0 {
				b.WriteString("    fullCoverageWith:\n")
				for _, cw := range r.fullCoverageWith {
					b.WriteString("      - " + cw + "\n")
				}
			}
		}
	}
	if len(defers) > 0 {
		b.WriteString("defers:\n")
		for _, d := range defers {
			b.WriteString("  - spec: " + d.spec + "\n")
			b.WriteString("    to: " + d.to + "\n")
		}
	}
	b.WriteString("---\n\n# A deferring plan\n\n## Tasks\n\n### Task 1 — Only task\n\n")
	b.WriteString("```yaml\nkind: chore\n```\n\nDo it.\n")
	return b.String()
}

// deferringPlan creates a plan deferring refs to their owners (and optionally
// covering others), accepting it when accept is set.
func deferringPlan(t *testing.T, s *Store, slug string, accept bool, defers []deferralRef, covers ...coverageRef) *model.Doc {
	t.Helper()
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: slug, Body: deferringPlanBody(defers, covers...), CreatedBy: "stig",
	})
	if !accept {
		return doc
	}
	accepted, _, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("accept plan %s: %v", slug, err)
	}
	return accepted
}

// gapAnchors renders anchor(coverage) for each of a gap's sections, in
// order, so tests can assert against a plain string slice. A deferred section
// carries its owner (026 §2.1, §5.3), rendered the way DocPlanningTable does:
// "sec-N(deferred:OWNER)".
func gapAnchors(gap model.DocPlanningGap) []string {
	out := make([]string, len(gap.Gaps))
	for i, s := range gap.Gaps {
		if s.Coverage == "deferred" && s.Owner != "" {
			out[i] = s.Anchor + "(deferred:" + s.Owner + ")"
		} else {
			out[i] = s.Anchor + "(" + s.Coverage + ")"
		}
	}
	return out
}

// needsPlanning runs the query and returns the one gap it expects, failing
// the test when the result does not name exactly the given specs.
func needsPlanningSlugs(t *testing.T, s *Store, project string) ([]string, []model.DocPlanningGap) {
	t.Helper()
	docs, gaps, err := s.NeedsPlanning(t.Context(), project)
	if err != nil {
		t.Fatalf("NeedsPlanning: %v", err)
	}
	slugs := make([]string, len(docs))
	for i, d := range docs {
		slugs[i] = d.Slug
	}
	return slugs, gaps
}

// TestDocNeedsPlanningReportsUncoveredSections: an accepted spec is listed
// with exactly the anchors no accepted plan's covers edge names, in document
// order, alongside its total section count.
func TestDocNeedsPlanningReportsUncoveredSections(t *testing.T) {
	s := openDocStore(t)
	spec := mustAcceptedSpec(t, s, "025-x")
	coveringPlan(t, s, "plan-a", true, "025-x#sec-1")

	slugs, gaps := needsPlanningSlugs(t, s, "p1")
	if !slices.Equal(slugs, []string{"025-x"}) {
		t.Fatalf("needs planning = %v, want [025-x]", slugs)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %v, want one entry", gaps)
	}
	got := gaps[0]
	if got.Doc != spec.ID {
		t.Errorf("gap doc = %d, want %d", got.Doc, spec.ID)
	}
	if got.Sections != 3 {
		t.Errorf("gap sections = %d, want 3", got.Sections)
	}
	if !slices.Equal(gapAnchors(got), []string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Errorf("gaps = %v, want [sec-2(unplanned) sec-2.1(unplanned)]", gapAnchors(got))
	}
}

// TestDocNeedsPlanningFullyCoveredSpecOmitted: every section named by some
// accepted plan takes the spec out of the set, and two plans naming the same
// section is legal and unremarked (026 §2.1).
func TestDocNeedsPlanningFullyCoveredSpecOmitted(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	coveringPlan(t, s, "plan-a", true, "025-x#sec-1", "025-x#sec-2")
	coveringPlan(t, s, "plan-b", true, "025-x#sec-2", "025-x#sec-2.1")

	slugs, gaps := needsPlanningSlugs(t, s, "p1")
	if len(slugs) != 0 || len(gaps) != 0 {
		t.Fatalf("needs planning = %v / %v, want empty", slugs, gaps)
	}
}

// TestDocNeedsPlanningDraftSpecNotOwedPlanning: 026 §2.1 — a draft spec is not
// yet a planning gap.
func TestDocNeedsPlanningDraftSpecNotOwedPlanning(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	slugs, _ := needsPlanningSlugs(t, s, "p1")
	if len(slugs) != 0 {
		t.Fatalf("needs planning = %v, want empty for a draft spec", slugs)
	}
}

// TestDocNeedsPlanningDraftPlanDoesNotCover: 026 §2.1 — a draft plan has not
// yet undertaken work, so its covers edges discharge nothing.
func TestDocNeedsPlanningDraftPlanDoesNotCover(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	coveringPlan(t, s, "plan-a", false, "025-x#sec-1", "025-x#sec-2", "025-x#sec-2.1")

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(unplanned)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want every anchor unplanned", gaps)
	}
}

// TestDocNeedsPlanningWholeDocumentEdgeCoversNothing: a covers edge with no
// fragment names no section, so it discharges none (026 §2.1 — it cannot say
// which present section it undertakes and would silently claim future ones).
func TestDocNeedsPlanningWholeDocumentEdgeCoversNothing(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	coveringPlan(t, s, "plan-a", true, "025-x")

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(unplanned)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want every anchor unplanned", gaps)
	}
}

// TestDocNeedsPlanningNoSpecSentinelCoversNothing: `covers: NO-SPEC` resolves
// to no document (026 §4.3), so it lands in to_external and contributes
// nothing — no special case needed. The plan itself is never a planning gap.
func TestDocNeedsPlanningNoSpecSentinelCoversNothing(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	coveringPlan(t, s, "plan-a", true, "NO-SPEC")

	slugs, gaps := needsPlanningSlugs(t, s, "p1")
	if !slices.Equal(slugs, []string{"025-x"}) {
		t.Fatalf("needs planning = %v, want the spec alone", slugs)
	}
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(unplanned)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want every anchor unplanned", gaps)
	}
}

// TestDocNeedsPlanningScopesToProject: an empty project answers over every
// project; a named one narrows to it.
func TestDocNeedsPlanningScopesToProject(t *testing.T) {
	s := openDocStore(t)
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO projects (id, name, key) VALUES ('p2','P2','P2')`); err != nil {
		t.Fatal(err)
	}
	mustAcceptedSpec(t, s, "025-x")
	other := mustCreateDoc(t, s, DocInput{
		Project: "p2", Kind: "spec", Number: 25, Slug: "025-y", Body: specBody, CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, other.ID, "stig"); err != nil {
		t.Fatalf("accept p2 spec: %v", err)
	}

	all, _ := needsPlanningSlugs(t, s, "")
	if !slices.Equal(all, []string{"025-x", "025-y"}) {
		t.Fatalf("unscoped needs planning = %v, want both specs", all)
	}
	scoped, _ := needsPlanningSlugs(t, s, "p2")
	if !slices.Equal(scoped, []string{"025-y"}) {
		t.Fatalf("p2 needs planning = %v, want [025-y]", scoped)
	}
}

// --- NeedsPlanning three-valued coverage (026 §2.1's outcome table) --------

// TestDocNeedsPlanningFullCoverageDischarges: an accepted plan claiming a
// section `full` discharges it; the sections no plan names stay unplanned.
func TestDocNeedsPlanningFullCoverageDischarges(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "full"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged, sec-2/sec-2.1 unplanned", gaps)
	}
}

// TestDocNeedsPlanningPartialWithNoClosureIsPartialGap: a `partial` claim
// with no fullCoverageWith set closes nothing, so the section stays a
// "partial" gap (026 §2.1).
func TestDocNeedsPlanningPartialWithNoClosureIsPartialGap(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "partial"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial", gaps)
	}
}

// TestDocNeedsPlanningPartialClosedByFullSiblingDischarges: fullCoverageWith
// naming an accepted plan that itself covers the same section `full` closes
// the claim, discharging the section (026 §2.1).
func TestDocNeedsPlanningPartialClosedByFullSiblingDischarges(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	sibling := levelledPlan(t, s, "plan-sibling", true, coverageRef{ref: "025-x#sec-1", level: "full"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{sibling.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged via fullCoverageWith", gaps)
	}
}

// TestDocNeedsPlanningPartialClosedByPartialSiblingDischarges: a
// fullCoverageWith sibling that itself only contributes `partial` still
// closes the claim (026 §2.1 asks only that it "contribute full or partial",
// not that its own claim be closed).
func TestDocNeedsPlanningPartialClosedByPartialSiblingDischarges(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	sibling := levelledPlan(t, s, "plan-sibling", true, coverageRef{ref: "025-x#sec-1", level: "partial"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{sibling.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged even though the sibling is only partial", gaps)
	}
}

// TestDocNeedsPlanningPartialClosureIgnoresDraftSibling: fullCoverageWith is
// checked, never taken on trust — a draft sibling closes nothing (026 §2.1).
func TestDocNeedsPlanningPartialClosureIgnoresDraftSibling(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	sibling := levelledPlan(t, s, "plan-sibling", false, coverageRef{ref: "025-x#sec-1", level: "full"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{sibling.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: a draft sibling closes nothing", gaps)
	}
}

// TestDocNeedsPlanningPartialClosureRequiresEveryNamedSibling: 026 §2.1's
// closure test is universal over the named plans, not existential — one
// qualifying sibling does not close the claim when a second sibling, named
// alongside it, is still a draft.
func TestDocNeedsPlanningPartialClosureRequiresEveryNamedSibling(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	accepted := levelledPlan(t, s, "plan-sibling-accepted", true,
		coverageRef{ref: "025-x#sec-1", level: "partial"})
	draft := levelledPlan(t, s, "plan-sibling-draft", false,
		coverageRef{ref: "025-x#sec-1", level: "partial"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial",
		fullCoverageWith: []string{accepted.Slug, draft.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: a draft among two named siblings blocks closure", gaps)
	}
}

// TestDocNeedsPlanningPartialClosureIgnoresNoneSibling: a fullCoverageWith
// sibling that itself claims `none` contributes nothing to the closure (026
// §2.1).
func TestDocNeedsPlanningPartialClosureIgnoresNoneSibling(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	sibling := levelledPlan(t, s, "plan-sibling", true, coverageRef{ref: "025-x#sec-1", level: "none"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{sibling.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: a none sibling closes nothing", gaps)
	}
}

// TestDocNeedsPlanningPartialClosureIgnoresSiblingCoveringDifferentSection:
// fullCoverageWith is scoped to the same section — a sibling that covers a
// different one of the spec's sections closes nothing for this one, even
// though it discharges its own (026 §2.1).
func TestDocNeedsPlanningPartialClosureIgnoresSiblingCoveringDifferentSection(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	sibling := levelledPlan(t, s, "plan-sibling", true, coverageRef{ref: "025-x#sec-2", level: "full"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{sibling.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial and sec-2 discharged on its own merits", gaps)
	}
}

// TestDocNeedsPlanningPartialClosureIgnoresUnresolvableReference: a
// fullCoverageWith entry this project cannot resolve is, by definition,
// unresolvable and closes nothing (026 §2.1).
func TestDocNeedsPlanningPartialClosureIgnoresUnresolvableReference(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{"nowhere-plan"},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: an unresolvable reference closes nothing", gaps)
	}
}

// TestDocNeedsPlanningNoneOnlyIsBoundOnlyGap: a section every accepted plan
// naming it claims `none` for is "bound-only" — acknowledged but not planned
// (026 §2.1).
func TestDocNeedsPlanningNoneOnlyIsBoundOnlyGap(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "none"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(bound-only)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 bound-only", gaps)
	}
}

// TestDocNeedsPlanningPartialByOneFullByAnotherDischarges: one plan's
// `partial` claim and another's `full` claim on the same section together
// discharge it (026 §2.1's outcome table).
func TestDocNeedsPlanningPartialByOneFullByAnotherDischarges(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "partial"})
	levelledPlan(t, s, "plan-b", true, coverageRef{ref: "025-x#sec-1", level: "full"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged by the full claim", gaps)
	}
}

// TestDocNeedsPlanningNoneByOneAndPartialByAnotherIsPartialGap: `partial`
// dominates `none` — one plan claiming `none` does not demote a section
// another plan claims `partial` down to "bound-only" (026 §2.1).
func TestDocNeedsPlanningNoneByOneAndPartialByAnotherIsPartialGap(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "none"})
	levelledPlan(t, s, "plan-b", true, coverageRef{ref: "025-x#sec-1", level: "partial"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: partial dominates bound-only", gaps)
	}
}

// TestDocNeedsPlanningSupersededPlanDischarges: 026 §2.1's amended discharging
// set is "accepted or superseded" — a superseded plan is one that was
// accepted and then carried out (025 §9), so its full claim still discharges
// the section it covered.
func TestDocNeedsPlanningSupersededPlanDischarges(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	plan := levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "full"})
	setDocStatus(t, s, plan.ID, "superseded")

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged: a superseded plan discharges what it covered", gaps)
	}
}

// --- NeedsPlanning defers classification (026 §2.1, §5.3) -----------------

// TestDocNeedsPlanningDeferredSectionReportsOwner: an accepted plan's defers
// entry reports the section "deferred" with its owner's slug; a section no
// plan names at all stays "unplanned" (026 §2.1, §5.3).
func TestDocNeedsPlanningDeferredSectionReportsOwner(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(deferred:owner-spec)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 deferred to owner-spec", gaps)
	}
}

// TestDocNeedsPlanningDeferredOutranksBoundOnly: with one plan bound by a
// section (`none`) and another deferring it, the section reports deferred —
// a deferral says who is owed the rest, not merely that the section was read
// (026 §2.1's precedence: partial > deferred > bound-only > unplanned).
func TestDocNeedsPlanningDeferredOutranksBoundOnly(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "none"})
	deferringPlan(t, s, "plan-b", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(deferred:owner-spec)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 deferred: deferred outranks bound-only", gaps)
	}
}

// TestDocNeedsPlanningTwoDeferralOwnersAggregated: §5.3's one-owner rule is
// per plan, so two plans may defer one section to two owners. The report
// aggregates them deterministically, comma-joined without a space — the CLI
// joins anchors with spaces, so a spaced separator would split the token.
func TestDocNeedsPlanningTwoDeferralOwnersAggregated(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-a", Body: specBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 7, Slug: "owner-b", Body: specBody, CreatedBy: "stig",
	})
	deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-a"}})
	deferringPlan(t, s, "plan-b", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-b"}})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(deferred:owner-a,owner-b)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 deferred to owner-a,owner-b", gaps)
	}
}

// TestDocNeedsPlanningDeferralDeliveredByCoveringPlan: a deferral is
// delivered by any plan discharging the section, not only by the named owner
// — once a second accepted plan covers the section `full`, it disappears
// from the gaps the same as any other discharged section (026 §2.1).
func TestDocNeedsPlanningDeferralDeliveredByCoveringPlan(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})
	levelledPlan(t, s, "plan-b", true, coverageRef{ref: "025-x#sec-1", level: "full"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged: a deferral is delivered by any covering plan", gaps)
	}
}

// TestDocNeedsPlanningPartialWithDeferralReportsPartial: a section claimed
// `partial` by one plan and deferred by another reports "partial" — partial
// outranks deferred in 026 §2.1's precedence.
func TestDocNeedsPlanningPartialWithDeferralReportsPartial(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})
	levelledPlan(t, s, "plan-b", true, coverageRef{ref: "025-x#sec-1", level: "partial"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: partial outranks deferred", gaps)
	}
}

// TestDocNeedsPlanningDraftPlanDeferralIgnored: a draft plan has not yet
// undertaken work, so its defers entries classify nothing (026 §2.1).
func TestDocNeedsPlanningDraftPlanDeferralIgnored(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	deferringPlan(t, s, "plan-a", false, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(unplanned)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 unplanned: a draft plan's deferral does not count", gaps)
	}
}

// TestDocNeedsPlanningSupersededPlanDeferralCounts: the deferring set is
// accepted or superseded, never draft (026 §5.3) — a superseded plan's
// deferral stands, being spent does not deliver a handoff the plan never
// made.
func TestDocNeedsPlanningSupersededPlanDeferralCounts(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	plan := deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})
	setDocStatus(t, s, plan.ID, "superseded")

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(deferred:owner-spec)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 still deferred: a superseded plan's deferral stands", gaps)
	}
}

// TestDocNeedsPlanningDeferralOwnerExternalVerbatim: an owner reference this
// project cannot resolve is reported verbatim, the same fallback
// fullCoverageWith uses (026 §2.1, §5.3).
func TestDocNeedsPlanningDeferralOwnerExternalVerbatim(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "999-nowhere-owner.md"}})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(deferred:999-nowhere-owner.md)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 deferred to the unresolved reference verbatim", gaps)
	}
}

// needsExecutionSlugs runs the query and returns the matching plans' slugs.
func needsExecutionSlugs(t *testing.T, s *Store, project string) []string {
	t.Helper()
	docs, err := s.NeedsExecution(t.Context(), project)
	if err != nil {
		t.Fatalf("NeedsExecution: %v", err)
	}
	slugs := make([]string, len(docs))
	for i, d := range docs {
		slugs[i] = d.Slug
	}
	return slugs
}

// TestDocNeedsExecutionOpenTask: an accepted plan with any non-closed task in
// its set is pending work.
func TestDocNeedsExecutionOpenTask(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "mint-plan", Body: planMintBody, CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}

	if got := needsExecutionSlugs(t, s, "p1"); !slices.Equal(got, []string{"mint-plan"}) {
		t.Fatalf("needs execution = %v, want [mint-plan]", got)
	}
}

// TestDocNeedsExecutionAllTasksClosed: once every task in the set is closed —
// delivered or abandoned, taskClosed's notion — the plan drops out.
func TestDocNeedsExecutionAllTasksClosed(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "mint-plan", Body: planMintBody, CreatedBy: "stig",
	})
	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	for i, task := range minted {
		if err := transition(t, s, taskTestNow, task.ID, "draft", "ready"); err != nil {
			t.Fatalf("ready %s: %v", task.ID, err)
		}
		if i == 0 {
			walkTo(t, s, task.ID, "abandoned")
			continue
		}
		walkTo(t, s, task.ID, "merged")
	}

	if got := needsExecutionSlugs(t, s, "p1"); len(got) != 0 {
		t.Fatalf("needs execution = %v, want empty once every task is closed", got)
	}
}

// TestDocNeedsExecutionDraftPlanOmitted: a draft plan has undertaken nothing.
func TestDocNeedsExecutionDraftPlanOmitted(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "mint-plan", Body: planMintBody, CreatedBy: "stig",
	})

	if got := needsExecutionSlugs(t, s, "p1"); len(got) != 0 {
		t.Fatalf("needs execution = %v, want empty for a draft plan", got)
	}
}

// TestDocNeedsExecutionUnmintedAcceptedPlanOmitted: the only accepted plans
// with no task set are the importer's spent plans, which are not pending work
// — the deliberate departure from 025 §18's "unminted or unfinished".
func TestDocNeedsExecutionUnmintedAcceptedPlanOmitted(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "spent-plan", Status: "accepted", CreatedBy: "stig",
		Body: "---\nstatus: accepted\n---\n\n# A spent plan\n\nAll done long ago.\n",
	})

	if got := needsExecutionSlugs(t, s, "p1"); len(got) != 0 {
		t.Fatalf("needs execution = %v, want empty for an unminted accepted plan", got)
	}
}

// TestDocNeedsExecutionScopesToProjectAndKind: accepted specs never appear,
// and a project narrows the set.
func TestDocNeedsExecutionScopesToProjectAndKind(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "mint-plan", Body: planMintBody, CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}

	if got := needsExecutionSlugs(t, s, ""); !slices.Equal(got, []string{"mint-plan"}) {
		t.Fatalf("needs execution = %v, want the plan alone", got)
	}
	if got := needsExecutionSlugs(t, s, "p2"); len(got) != 0 {
		t.Fatalf("needs execution in p2 = %v, want empty", got)
	}
}

// --- BareSupersededSections (025 §6 rule 2) ---------------------------------

// bareSupersededSlugs runs the query unscoped by kind and returns the
// matching documents' slugs alongside their gaps, in the same order.
func bareSupersededSlugs(t *testing.T, s *Store, project string) ([]string, []model.DocSupersessionGap) {
	t.Helper()
	return bareSupersededKindSlugs(t, s, project, "")
}

// bareSupersededKindSlugs is bareSupersededSlugs with a kind narrowing.
func bareSupersededKindSlugs(t *testing.T, s *Store, project, kind string) ([]string, []model.DocSupersessionGap) {
	t.Helper()
	docs, gaps, err := s.BareSupersededSections(t.Context(), project, kind)
	if err != nil {
		t.Fatalf("BareSupersededSections: %v", err)
	}
	slugs := make([]string, len(docs))
	for i, d := range docs {
		slugs[i] = d.Slug
	}
	return slugs, gaps
}

// TestDocBareSupersededNoReplacesEdge: a superseded spec with no replaces
// edge naming it at all is reported in full, anchors in document order.
func TestDocBareSupersededNoReplacesEdge(t *testing.T) {
	s := openDocStore(t)
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if !slices.Equal(slugs, []string{"006-old"}) {
		t.Fatalf("bare superseded = %v, want [006-old]", slugs)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %v, want one entry", gaps)
	}
	got := gaps[0]
	if got.Doc != old.ID {
		t.Errorf("gap doc = %d, want %d", got.Doc, old.ID)
	}
	if got.Sections != 3 {
		t.Errorf("gap sections = %d, want 3", got.Sections)
	}
	if !slices.Equal(got.Unexplained, []string{"sec-1", "sec-2", "sec-2.1"}) {
		t.Errorf("unexplained = %v, want every anchor", got.Unexplained)
	}
}

// TestDocBareSupersededDocumentLevelEdgeExplainsWholeDoc: a document-scoped
// replaces edge discharges every section of the document it names, even
// though the successor never names one of them by anchor.
func TestDocBareSupersededDocumentLevelEdgeExplainsWholeDoc(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\na\n",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if len(slugs) != 0 || len(gaps) != 0 {
		t.Fatalf("bare superseded = %v / %v, want empty", slugs, gaps)
	}
}

// TestDocBareSupersededSectionEdgesExplainEverySection: a section-scoped
// replaces edge naming each of the superseded document's anchors leaves
// nothing unexplained.
func TestDocBareSupersededSectionEdgesExplainEverySection(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md#sec-1\n" +
			"    - 006-old.md#sec-2\n    - 006-old.md#sec-2.1\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\na\n",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if len(slugs) != 0 || len(gaps) != 0 {
		t.Fatalf("bare superseded = %v / %v, want empty", slugs, gaps)
	}
}

// TestDocBareSupersededSomeSectionsExplained: only the anchors no
// section-scoped edge names are reported; Sections stays the document's full
// count.
func TestDocBareSupersededSomeSectionsExplained(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md#sec-1\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\na\n",
	})

	_, gaps := bareSupersededSlugs(t, s, "p1")
	if len(gaps) != 1 {
		t.Fatalf("gaps = %v, want one entry", gaps)
	}
	if gaps[0].Sections != 3 {
		t.Errorf("gap sections = %d, want 3", gaps[0].Sections)
	}
	if !slices.Equal(gaps[0].Unexplained, []string{"sec-2", "sec-2.1"}) {
		t.Errorf("unexplained = %v, want [sec-2 sec-2.1]", gaps[0].Unexplained)
	}
}

// TestDocBareSupersededOnlySupersededDocsReported: an accepted and a draft
// document are never reported, whatever edges name them.
func TestDocBareSupersededOnlySupersededDocsReported(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-accepted", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 7, Slug: "007-draft", Body: specBody,
		CreatedBy: "stig",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if len(slugs) != 0 || len(gaps) != 0 {
		t.Fatalf("bare superseded = %v / %v, want empty", slugs, gaps)
	}
}

// TestDocBareSupersededExternalEdgeDoesNotExplain: a replaces reference that
// resolves to no row in this project lands in to_external and explains
// nothing — the superseded document it would have named is still reported in
// full.
func TestDocBareSupersededExternalEdgeDoesNotExplain(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 999-nowhere.md#sec-1\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\na\n",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if !slices.Equal(slugs, []string{"006-old"}) {
		t.Fatalf("bare superseded = %v, want [006-old]", slugs)
	}
	if len(gaps) != 1 || !slices.Equal(gaps[0].Unexplained, []string{"sec-1", "sec-2", "sec-2.1"}) {
		t.Fatalf("gaps = %v, want every anchor unexplained", gaps)
	}
}

// TestDocBareSupersededScopesToProject: an empty project answers over every
// project; a named one narrows to it.
func TestDocBareSupersededScopesToProject(t *testing.T) {
	s := openDocStore(t)
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO projects (id, name, key) VALUES ('p2','P2','P2')`); err != nil {
		t.Fatal(err)
	}
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p2", Kind: "spec", Number: 6, Slug: "006-old-2", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})

	all, _ := bareSupersededSlugs(t, s, "")
	if !slices.Equal(all, []string{"006-old", "006-old-2"}) {
		t.Fatalf("unscoped bare superseded = %v, want both docs", all)
	}
	scoped, _ := bareSupersededSlugs(t, s, "p2")
	if !slices.Equal(scoped, []string{"006-old-2"}) {
		t.Fatalf("p2 bare superseded = %v, want [006-old-2]", scoped)
	}
}

// TestDocBareSupersededPlanNeverReported: a plan carries no sections (025
// §9), so it can never appear here even when superseded.
func TestDocBareSupersededPlanNeverReported(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "old-plan", CreatedBy: "stig", Status: "superseded",
		Body: "---\nstatus: superseded\n---\n\n# An old plan\n\nSpent.\n",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if len(slugs) != 0 || len(gaps) != 0 {
		t.Fatalf("bare superseded = %v / %v, want empty for a superseded plan", slugs, gaps)
	}
}

// TestDocBareSupersededKindNarrows: kind narrows the same way project does —
// "" answers both a superseded spec and a superseded ADR, "spec" or "adr"
// answers only its own.
func TestDocBareSupersededKindNarrows(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old-spec", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "adr", Number: 7, Slug: "007-old-adr", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})

	if got, _ := bareSupersededKindSlugs(t, s, "p1", ""); !slices.Equal(got, []string{"006-old-spec", "007-old-adr"}) {
		t.Fatalf("unscoped bare superseded = %v, want both docs", got)
	}
	if got, _ := bareSupersededKindSlugs(t, s, "p1", "spec"); !slices.Equal(got, []string{"006-old-spec"}) {
		t.Fatalf("spec-scoped bare superseded = %v, want [006-old-spec]", got)
	}
	if got, _ := bareSupersededKindSlugs(t, s, "p1", "adr"); !slices.Equal(got, []string{"007-old-adr"}) {
		t.Fatalf("adr-scoped bare superseded = %v, want [007-old-adr]", got)
	}
}

// TestDocBareSupersededViaAcceptPath: supersedeReplacedDocs flips a target on
// from_anchor IS NULL (a document-level source), not on to_anchor — so a
// document-level source naming a section-scoped target flips the whole
// target document to superseded while its `replaces` edge only names one of
// its sections. That leaves the other two sections bare, reachable through
// the real accept path rather than a fixture that sets Status directly.
func TestDocBareSupersededViaAcceptPath(t *testing.T) {
	s := openDocStore(t)
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	successor := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md#sec-1\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\na\n",
	})

	if _, _, err := acceptDoc(t, s, successor.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc(025-new): %v", err)
	}

	got, err := s.GetDoc(t.Context(), old.ID)
	if err != nil {
		t.Fatalf("GetDoc(006-old): %v", err)
	}
	if got.Status != "superseded" {
		t.Fatalf("006-old status = %q, want superseded", got.Status)
	}

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if !slices.Equal(slugs, []string{"006-old"}) {
		t.Fatalf("bare superseded = %v, want [006-old]", slugs)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %v, want one entry", gaps)
	}
	if gaps[0].Sections != 3 {
		t.Errorf("gap sections = %d, want 3", gaps[0].Sections)
	}
	if !slices.Equal(gaps[0].Unexplained, []string{"sec-2", "sec-2.1"}) {
		t.Errorf("unexplained = %v, want [sec-2 sec-2.1]", gaps[0].Unexplained)
	}
}

// replacerBody is a minimal accepted-at-create spec whose document-level
// `replaces` names ref. The corpus importer's shape: status in the
// frontmatter, no separate accept.
func replacerBody(title, ref string) string {
	return "---\nstatus: accepted\nissued: 2026-08-01\nreplaces:\n  \".\":\n    - " + ref +
		"\n---\n\n# " + title + "\n\n## 1. Scope {#sec-1}\n\nBody.\n"
}

// docStatus reads one document's stored status, which is the thing the
// cascade moves — CreateDoc's return value is a projection of the same row,
// asserted separately where it matters.
func docStatus(t *testing.T, s *Store, id int64) string {
	t.Helper()
	d, err := s.GetDoc(t.Context(), id)
	if err != nil {
		t.Fatalf("GetDoc(%d): %v", id, err)
	}
	return d.Status
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
	if !slices.Equal(before, want) {
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
	if !slices.Equal(after, wantAfter) {
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
	if !slices.Equal(before, want) {
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
	if !slices.Equal(after, wantAfter) {
		t.Fatalf("edges after repair = %+v, want %+v", after, wantAfter)
	}
	if got := docStatus(t, s, old.ID); got != "superseded" {
		t.Fatalf("006-old status after repair = %q, want superseded", got)
	}
}

// TestDocIRIRoundTrip: DocIRI renders spec 025 §4.1's project-qualified
// subject IRI for a spec, an ADR, and a plan in the same project, and
// DocBySubjectIRI resolves each back to its row. An unknown IRI is
// ErrNotFound.
func TestDocIRIRoundTrip(t *testing.T) {
	s := openDocStore(t)
	ctx := t.Context()

	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	adr := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "adr", Number: 7,
		Slug: "007-some-decision", Body: specBody, CreatedBy: "stig",
	})
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan",
		Slug: "025-documents-in-the-backbone-2", Body: planBody, CreatedBy: "stig",
	})

	cases := []struct {
		doc  *model.Doc
		want string
	}{
		{spec, "wlid:doc/spec-p1-025"},
		{adr, "wlid:doc/adr-p1-007"},
		{plan, "wlid:doc/plan-p1-025-documents-in-the-backbone-2"},
	}
	for _, tc := range cases {
		if got := DocIRI(*tc.doc); got != tc.want {
			t.Errorf("DocIRI(%s) = %q, want %q", tc.doc.Slug, got, tc.want)
		}
		resolved, err := s.DocBySubjectIRI(ctx, tc.want)
		if err != nil {
			t.Fatalf("DocBySubjectIRI(%q): %v", tc.want, err)
		}
		if resolved.ID != tc.doc.ID {
			t.Fatalf("DocBySubjectIRI(%q) = doc %d, want doc %d", tc.want, resolved.ID, tc.doc.ID)
		}
	}

	if _, err := s.DocBySubjectIRI(ctx, "wlid:doc/spec-p1-999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DocBySubjectIRI unknown iri: got %v, want ErrNotFound", err)
	}
}

// --- CreateDoc re-points references that named it before it existed (WL-130) -

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
		if got := docEdges(t, s, plan.ID); !slices.Equal(got, want(spec.ID)) {
			t.Fatalf("edges = %+v, want %+v", got, want(spec.ID))
		}
	})

	t.Run("spec then plan", func(t *testing.T) {
		s := openDocStore(t)
		spec := newSpec(t, s)
		plan := newPlan(t, s)
		if got := docEdges(t, s, plan.ID); !slices.Equal(got, want(spec.ID)) {
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
	if got := docEdges(t, s, plan.ID); !slices.Equal(got, want) {
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
	if got := docEdges(t, s, other.ID); !slices.Equal(got, before) {
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

// TestFrontmatterEdgesBlockedByBecomesInverseBlocks: the spelling is resolved
// where the frontmatter is read, not where the row is written — `blockedBy`
// leaves frontmatterEdges as a `blocks` edge marked inverse, so every guard
// and every dedupe downstream sees one relation with one type and only the
// row's two ends move (025 §5, WL-143). No database: this is the translation
// itself.
func TestFrontmatterEdgesBlockedByBecomesInverseBlocks(t *testing.T) {
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

// TestDocEdgeTypesWithoutWriter pins the one gap between the doc_edges type
// set and what can produce a row in it. rebuildEdges derives every edge from
// frontmatter through frontmatterEdges, which resolves `blockedBy` to a
// `blocks` edge and otherwise records designdoc.ActingRels, so a type outside
// that set is a value the CHECK admits and no surface writes.
//
// Today that is exactly `implements`, and deliberately so: 026 §5.1 makes
// `implements` the retired frontmatter spelling of `covers` (read as covers,
// never written as implements), while the `implements` *edge type* is reserved
// for a component's evidence about its own code (026 §6.2) — a different
// subject, declared in `.worklode/implements.yaml`, whose machinery is 025 §11
// and is not built. WL-132 filed that gap so it is not re-diagnosed as a
// defect; this test is the record. When §11's writer lands, or a new type
// joins docEdgeInverse, update the want list deliberately.
func TestDocEdgeTypesWithoutWriter(t *testing.T) {
	var unwritten []string
	for typ := range docEdgeInverse {
		if !slices.Contains(designdoc.ActingRels, typ) {
			unwritten = append(unwritten, typ)
		}
	}
	slices.Sort(unwritten)
	if want := []string{"implements"}; !slices.Equal(unwritten, want) {
		t.Errorf("doc_edges types with no writer = %v, want %v", unwritten, want)
	}
}

// seedDocsTask inserts a task in the doc tests' project, for the authoring
// edge 025 §12 asks for. Direct SQL, like seedDocsProject: what is under test
// is the docs row, not how the task got there.
func seedDocsTask(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO tasks (id, project_id, title, priority, kind, state, created_at, updated_at)
		 VALUES ($1, 'p1', 'Write the spec', 'medium', 'design', 'in_progress', now(), now())`,
		id); err != nil {
		t.Fatal(err)
	}
}

// TestCreateDocRecordsGeneratedByTask pins 025 §12's authorship edge at the
// store: the task that wrote a document is persisted and read back, a create
// naming no task leaves it unset rather than failing, and a create naming a
// task that does not exist is refused with a message pointing at the field.
func TestCreateDocRecordsGeneratedByTask(t *testing.T) {
	s := openDocStore(t)
	seedDocsTask(t, s, "P1-1")

	authored := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x",
		Body: specBody, CreatedBy: "stig", GeneratedByTask: "P1-1",
	})
	if authored.GeneratedByTask != "P1-1" {
		t.Errorf("GeneratedByTask = %q, want P1-1", authored.GeneratedByTask)
	}
	// Read back through scanDoc, which is what every later query uses.
	got, err := s.GetDoc(t.Context(), authored.ID)
	if err != nil {
		t.Fatalf("GetDoc: %v", err)
	}
	if got.GeneratedByTask != "P1-1" {
		t.Errorf("GetDoc GeneratedByTask = %q, want P1-1", got.GeneratedByTask)
	}

	// Nullable by design: a document nothing claimed a task for is a normal
	// state, the same way tasks.plan_doc and tasks.about_doc are nullable.
	unauthored := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "adr", Number: 51, Slug: "051-x",
		Body: specBody, CreatedBy: "stig",
	})
	if unauthored.GeneratedByTask != "" {
		t.Errorf("GeneratedByTask = %q, want empty", unauthored.GeneratedByTask)
	}

	_, err = createDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 26, Slug: "026-x",
		Body: specBody, CreatedBy: "stig", GeneratedByTask: "P1-404",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown authoring task: err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "P1-404") {
		t.Errorf("err = %q, want it to name the task", err)
	}
}
