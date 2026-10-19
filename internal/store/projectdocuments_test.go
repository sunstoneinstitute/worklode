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

// TestIdeaSources covers an idea's register rows (WL-REQ-1798): every read
// and write needs a shared idea, so a private one is ErrNotShared to its
// author and ErrNotFound to anyone else, and its rows never reach a
// project's list.
func TestIdeaSources(t *testing.T) {
	t.Parallel()
	s, ctx := openIdeaStore(t)
	idea, err := s.CreateIdea(ctx, "a", model.CreateIdeaInput{Title: "Rivers", Type: "general"})
	if err != nil {
		t.Fatal(err)
	}
	in := model.CreateProjectDocumentInput{Name: "Census", Category: "sources", Type: "dataset", Status: "draft"}

	for _, c := range []struct {
		caller, id string
		want       error
	}{{"a", idea.ID, ErrNotShared}, {"b", idea.ID, ErrNotFound}, {"", idea.ID, ErrNotFound}, {"a", "idea_nope", ErrNotFound}} {
		if _, err := s.ListIdeaDocuments(ctx, c.caller, c.id); !errors.Is(err, c.want) {
			t.Errorf("%q list %s = %v, want %v", c.caller, c.id, err, c.want)
		}
		if _, err := s.CreateProjectDocument(ctx, IdeaOwner(c.id), in, c.caller); !errors.Is(err, c.want) {
			t.Errorf("%q create %s = %v, want %v", c.caller, c.id, err, c.want)
		}
	}

	if _, err := s.ShareIdea(ctx, "a", idea.ID, "intake"); err != nil {
		t.Fatal(err)
	}
	if l, err := s.ListIdeaDocuments(ctx, "b", idea.ID); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("empty list = %#v, %v", l, err)
	}
	d, err := s.CreateProjectDocument(ctx, IdeaOwner(idea.ID), in, "b")
	if err != nil || d.Idea != idea.ID || d.Project != "" {
		t.Fatalf("create = %+v, %v", d, err)
	}
	if l, err := s.ListIdeaDocuments(ctx, "a", idea.ID); err != nil || len(l) != 1 || l[0].ID != d.ID {
		t.Fatalf("list = %+v, %v", l, err)
	}
	if l, err := s.ListProjectDocuments(ctx, "intake"); err != nil || len(l) != 0 {
		t.Fatalf("intake project list = %+v, %v", l, err)
	}
	final := "final"
	if got, err := s.PatchProjectDocument(ctx, d.ID, model.PatchProjectDocumentInput{Status: &final}, "a"); err != nil || got.Status != "final" {
		t.Fatalf("patch = %+v, %v", got, err)
	}
	if err := s.DeleteProjectDocument(ctx, d.ID, "b"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// TestPrivateIdeaSourceWrites covers PATCH and DELETE on a row whose idea
// is private: the owner idea decides, as for the idea routes.
func TestPrivateIdeaSourceWrites(t *testing.T) {
	t.Parallel()
	s, ctx := openIdeaStore(t)
	idea, err := s.CreateIdea(ctx, "a", model.CreateIdeaInput{Title: "Rivers", Type: "general"})
	if err != nil {
		t.Fatal(err)
	}
	// A row under a private idea cannot be made through the store's API, so
	// the test plants one.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO project_documents (id, idea, name, category, type, status, created_at, updated_at)
		VALUES ('doc-private', $1, 'n', 'sources', 'link', 'draft', now(), now())`, idea.ID); err != nil {
		t.Fatal(err)
	}
	final := "final"
	for caller, want := range map[string]error{"a": ErrNotShared, "b": ErrNotFound, "": ErrNotFound} {
		if _, err := s.PatchProjectDocument(ctx, "doc-private", model.PatchProjectDocumentInput{Status: &final}, caller); !errors.Is(err, want) {
			t.Errorf("%q patch = %v, want %v", caller, err, want)
		}
		if err := s.DeleteProjectDocument(ctx, "doc-private", caller); !errors.Is(err, want) {
			t.Errorf("%q delete = %v, want %v", caller, err, want)
		}
	}
}
