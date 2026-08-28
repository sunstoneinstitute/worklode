package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// seedDocsActor inserts the actor docs rows reference as creator/owner.
func seedDocsActor(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO actors (id, kind) VALUES ($1,'human')`, id); err != nil {
		t.Fatal(err)
	}
}

// openDocStore opens a store with the project and the two actors the doc
// tests write as created_by/owner.
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

// docVersionRow is one archived doc_versions row, for tests that check the
// snapshot table directly rather than through ListDocVersions/GetDocVersion.
type docVersionRow struct {
	version int
	title   string
	body    string
}

// docVersionRows reads a document's doc_versions rows in version order.
func docVersionRows(t *testing.T, s *Store, docID int64) []docVersionRow {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT version, title, body FROM doc_versions WHERE doc_id = $1 ORDER BY version`, docID)
	if err != nil {
		t.Fatalf("read doc_versions: %v", err)
	}
	defer rows.Close()
	var out []docVersionRow
	for rows.Next() {
		var r docVersionRow
		if err := rows.Scan(&r.version, &r.title, &r.body); err != nil {
			t.Fatalf("scan doc_versions row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read doc_versions: %v", err)
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

// Since 029 §4 every kind carries a number, plans included: the rule migration
// 0037 split by kind is now one rule for the whole corpus, so a plan row is an
// ordinary numbered row.
func TestDocSchemaPlanRowCarriesANumber(t *testing.T) {
	s := openTestStore(t)
	seedDocsProject(t, s)

	id, err := insertDoc(t, s, "plan", 7, "documents-in-the-backbone-2")
	if err != nil {
		t.Fatalf("insert numbered plan row: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a generated id")
	}
}

// The schema half of 029 §4: no document goes without a number, whatever its
// kind and whichever writer went around the store.
func TestDocSchemaNumberIsRequiredForEveryKind(t *testing.T) {
	for _, kind := range []string{"spec", "adr", "plan"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t)
			seedDocsProject(t, s)

			_, err := insertDoc(t, s, kind, nil, "no-number-"+kind)
			if err == nil {
				t.Fatal("expected a NOT NULL violation, got nil error")
			}
			if !strings.Contains(err.Error(), "not-null") {
				t.Fatalf("expected docs.number NOT NULL violation, got: %v", err)
			}
		})
	}
}

// A project's plan numbers are unique the way its spec numbers are: migration
// 0052 replaced the partial index with a plain one once the column stopped
// being nullable.
func TestDocSchemaPlanNumberIsUniquePerProject(t *testing.T) {
	s := openTestStore(t)
	seedDocsProject(t, s)

	if _, err := insertDoc(t, s, "plan", 7, "first-plan"); err != nil {
		t.Fatalf("insert first plan: %v", err)
	}
	_, err := insertDoc(t, s, "plan", 7, "second-plan")
	if err == nil {
		t.Fatal("expected a unique violation on (project, kind, number), got nil error")
	}
	if !isUniqueViolationOn(err, "docs_project_kind_number") {
		t.Fatalf("expected docs_project_kind_number unique violation, got: %v", err)
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
		Project: "p1", Kind: "spec", Number: -1, Slug: "bad", Body: specBody, CreatedBy: "stig",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("create", "error")); got != 1 {
		t.Fatalf("doc_operations{create,error} = %v, want 1", got)
	}

	// Owner transfer (025 §7.3) records under its own verb too.
	if _, _, err := transferDocOwner(t, s, created.ID, "ada", "stig"); err != nil {
		t.Fatalf("TransferDocOwner: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("transfer", "ok")); got != 1 {
		t.Fatalf("doc_operations{transfer,ok} = %v, want 1", got)
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

// transferDocOwner runs TransferDocOwner through RecordDocEvent, the way the
// API will, and returns the event id the caller can look up to check what
// landed.
func transferDocOwner(t *testing.T, s *Store, id int64, newOwner, actor string) (*model.Doc, int64, error) {
	t.Helper()
	var out *model.Doc
	eventID, _, err := s.RecordDocEvent(t.Context(), "transfer", "cli",
		fmt.Sprintf("doc-transfer-%d", docEventSeq.Add(1)), "doc.owner_changed", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, err = TransferDocOwner(tx, s.Now(), id, newOwner, actor, eventID)
			return err
		})
	return out, eventID, err
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

// TestDocAcceptDraftSpec: the owner's accept flips the status, freezes the
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

// TestDocAcceptWrongActorForbidden: acceptance is the owner's act (025 §7).
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
		t.Errorf("err = %v, want it to name the owner", err)
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

// TestDocTransferOwner: the owner may hand the document to another actor
// (025 §7.3), which lands as a doc.owner_changed event and a state_log entry
// naming the old and new owner.
func TestDocTransferOwner(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	got, eventID, err := transferDocOwner(t, s, doc.ID, "ada", "stig")
	if err != nil {
		t.Fatalf("TransferDocOwner: %v", err)
	}
	if got.Owner != "ada" {
		t.Errorf("owner = %q, want ada", got.Owner)
	}

	ev, err := s.GetEvent(t.Context(), eventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if ev.Type != "doc.owner_changed" {
		t.Errorf("event type = %q, want doc.owner_changed", ev.Type)
	}

	entries, err := s.StateLogForEntity(t.Context(), "doc", strconv.FormatInt(doc.ID, 10))
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	last := entries[len(entries)-1]
	if !strings.Contains(last.Change, `"field": "owner"`) ||
		!strings.Contains(last.Change, `"old": "stig"`) || !strings.Contains(last.Change, `"new": "ada"`) {
		t.Errorf("state log entry = %q, want field/old/new naming the transfer", last.Change)
	}
}

// TestDocTransferOwnerAdminNotOwner: an admin may transfer a document it does
// not own (025 §7.3) — the mechanism a document whose owner left the org is
// rescued through.
func TestDocTransferOwnerAdminNotOwner(t *testing.T) {
	s := openDocStore(t)
	if err := s.CreateActor(t.Context(), "root", "human", "root", true); err != nil {
		t.Fatalf("create admin actor: %v", err)
	}
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	got, _, err := transferDocOwner(t, s, doc.ID, "ada", "root")
	if err != nil {
		t.Fatalf("TransferDocOwner: %v", err)
	}
	if got.Owner != "ada" {
		t.Errorf("owner = %q, want ada", got.Owner)
	}
}

// TestDocTransferOwnerThirdPartyForbidden: neither the owner nor an admin
// refuses with ErrForbidden.
func TestDocTransferOwnerThirdPartyForbidden(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	_, _, err := transferDocOwner(t, s, doc.ID, "ada", "ada")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if got, err := s.GetDoc(t.Context(), doc.ID); err != nil || got.Owner != "stig" {
		t.Fatalf("doc owner = %+v, %v; want it still stig", got, err)
	}
}

// TestDocTransferOwnerSelfNoop: transferring to the actor that already owns
// the document is a legal no-op, not a refusal — Task 5's bulk transfer loops
// this endpoint over many documents and relies on re-runs being safe.
func TestDocTransferOwnerSelfNoop(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	got, _, err := transferDocOwner(t, s, doc.ID, "stig", "stig")
	if err != nil {
		t.Fatalf("TransferDocOwner: %v", err)
	}
	if got.Owner != "stig" {
		t.Errorf("owner = %q, want stig", got.Owner)
	}
	entries, err := s.StateLogForEntity(t.Context(), "doc", strconv.FormatInt(doc.ID, 10))
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("state log entries = %d, want 1 (no-op writes nothing new)", len(entries))
	}
}

// TestDocTransferOwnerUnknownActor: the new owner must be an existing actor
// (owner REFERENCES actors), surfaced as ErrInvalidInput naming the field
// rather than a raw constraint failure.
func TestDocTransferOwnerUnknownActor(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	_, _, err := transferDocOwner(t, s, doc.ID, "nobody", "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
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

// setDocStatus forces a document's status, standing in for the states only
// another act — a supersession, a corpus import — can produce.
func setDocStatus(t *testing.T, s *Store, id int64, status string) {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE docs SET status = $2 WHERE id = $1`, id, status); err != nil {
		t.Fatal(err)
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

// blockingPlanBody renders a draft plan ordering itself before blocked.
func blockingPlanBody(blocked string) string {
	return "---\nstatus: draft\nblocks: " + blocked + "\n---\n\n# Plan\n"
}

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
