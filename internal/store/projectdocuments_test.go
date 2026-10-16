package store

import (
	"context"
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestProjectDocumentsCRUD(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "p1", "Project One", "P1"); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"ada", "bob"} {
		if err := s.EnsureActor(ctx, a, "human", a); err != nil {
			t.Fatal(err)
		}
	}

	d, err := s.CreateProjectDocument(ctx, DocOwner{project: "p1"}, model.CreateProjectDocumentInput{
		Name: "Kickoff", Category: "meeting-notes", Type: "document", Status: "draft",
		Date: "2026-10-01", Participants: []string{"ada", "bob"},
	}, "ada")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.Project != "p1" || d.Idea != "" || d.CreatedBy != "ada" {
		t.Fatalf("unexpected row %+v", d)
	}

	list, err := s.ListProjectDocuments(ctx, "p1")
	if err != nil || len(list) != 1 || list[0].ID != d.ID || len(list[0].Participants) != 2 || list[0].Date != "2026-10-01" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if ideas, err := s.ListIdeaDocuments(ctx, "nope"); err != nil || len(ideas) != 0 {
		t.Fatalf("idea list = %+v, %v", ideas, err)
	}

	final := "final"
	got, err := s.PatchProjectDocument(ctx, d.ID, model.PatchProjectDocumentInput{Status: &final}, "bob")
	if err != nil || got.Status != "final" || got.Name != "Kickoff" {
		t.Fatalf("patch = %+v, %v", got, err)
	}
	re, _ := s.GetProjectDocument(ctx, d.ID)
	if re.Status != "final" {
		t.Fatalf("status not persisted: %+v", re)
	}

	// Validation errors name the field.
	badStatus := "done"
	if _, err := s.PatchProjectDocument(ctx, d.ID, model.PatchProjectDocumentInput{Status: &badStatus}, "bob"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad status: %v", err)
	}
	ghost := []string{"ghost"}
	if _, err := s.PatchProjectDocument(ctx, d.ID, model.PatchProjectDocumentInput{Participants: &ghost}, "bob"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown participant on patch: %v", err)
	}
	if _, err := s.CreateProjectDocument(ctx, DocOwner{project: "p1"}, model.CreateProjectDocumentInput{
		Name: "x", Category: "sources", Type: "link", Status: "draft", Participants: []string{"ghost"},
	}, "ada"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown participant on create: %v", err)
	}
	if _, err := s.CreateProjectDocument(ctx, DocOwner{project: "p1"}, model.CreateProjectDocumentInput{
		Name: "x", Category: "sources", Type: "link", Status: "draft", Due: "10/01",
	}, "ada"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad due: %v", err)
	}
	if _, err := s.CreateProjectDocument(ctx, DocOwner{}, model.CreateProjectDocumentInput{
		Name: "x", Category: "sources", Type: "link", Status: "draft",
	}, "ada"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("no owner: %v", err)
	}

	if err := s.DeleteProjectDocument(ctx, d.ID, "ada"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetProjectDocument(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if err := s.DeleteProjectDocument(ctx, d.ID, "ada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}

	for typ, want := range map[string]int{"document.registered": 1, "document.updated": 1, "document.removed": 1} {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE type = $1`, typ).Scan(&n); err != nil || n != want {
			t.Fatalf("events %s = %d, %v; want %d", typ, n, err, want)
		}
	}
}
