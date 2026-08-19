package store

import (
	"context"
	"testing"
	"time"
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
