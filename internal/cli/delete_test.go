package cli_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// deleteStub records the method, request URI and body of the last request and
// answers with a fixed task/doc envelope carrying a tombstone, which is enough
// for every wiring assertion below without a store behind it.
func deleteStub(t *testing.T) (*cli.Client, *struct{ Method, URI, Body string }) {
	t.Helper()
	got := &struct{ Method, URI, Body string }{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.Method, got.URI, got.Body = r.Method, r.URL.RequestURI(), string(body)
		w.Header().Set("Content-Type", "application/json")
		// A doc id is numeric and a task id is not, so the envelope has to
		// follow the route or the decode fails on the id alone.
		id := `"WL-7"`
		if strings.HasPrefix(r.URL.Path, "/api/v1/docs") {
			id = "12"
		}
		w.Write([]byte(`{"id":` + id + `,"slug":"deletable",` +
			`"tombstone":{"deleted_at":"2026-01-02T03:04:05Z","deleted_by":"alice","justification":"seeded by mistake"}}`))
	}))
	t.Cleanup(srv.Close)
	return cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "t"}), got
}

// TestDeleteTaskRequest pins DELETE /api/v1/tasks/{id} and that the
// justification travels in the body — including the empty one, which the
// server (not this client) decides about (044 §3).
func TestDeleteTaskRequest(t *testing.T) {
	c, got := deleteStub(t)

	task, _, err := c.DeleteTask(context.Background(), "WL-7", "seeded by mistake")
	if err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if got.Method != http.MethodDelete || got.URI != "/api/v1/tasks/WL-7" {
		t.Fatalf("DeleteTask hit %s %s, want DELETE /api/v1/tasks/WL-7", got.Method, got.URI)
	}
	if got.Body != `{"justification":"seeded by mistake"}` {
		t.Fatalf("DeleteTask body = %s", got.Body)
	}
	if task.Tombstone == nil || task.Tombstone.DeletedBy != "alice" {
		t.Fatalf("DeleteTask decoded tombstone = %+v", task.Tombstone)
	}

	// A blank justification still sends a body: the CLI must not pre-validate
	// it, and a dev instance accepts it (044 §3).
	if _, _, err := c.DeleteTask(context.Background(), "WL-7", ""); err != nil {
		t.Fatalf("DeleteTask with no justification: %v", err)
	}
	if got.Body != `{}` {
		t.Fatalf("DeleteTask body with no justification = %q, want {}", got.Body)
	}

	// A task id with a character the path would otherwise eat is escaped.
	if _, _, err := c.DeleteTask(context.Background(), "WL/7", "x"); err != nil {
		t.Fatalf("DeleteTask with an escapable id: %v", err)
	}
	if got.URI != "/api/v1/tasks/WL%2F7" {
		t.Fatalf("DeleteTask URI = %q, want the id path-escaped", got.URI)
	}
}

// TestUndeleteTaskRequest pins POST /api/v1/tasks/{id}/undelete, with no body
// to justify anything (044 §3).
func TestUndeleteTaskRequest(t *testing.T) {
	c, got := deleteStub(t)

	if _, _, err := c.UndeleteTask(context.Background(), "WL-7"); err != nil {
		t.Fatalf("UndeleteTask: %v", err)
	}
	if got.Method != http.MethodPost || got.URI != "/api/v1/tasks/WL-7/undelete" {
		t.Fatalf("UndeleteTask hit %s %s, want POST /api/v1/tasks/WL-7/undelete", got.Method, got.URI)
	}
	if got.Body != "" {
		t.Fatalf("UndeleteTask body = %q, want none", got.Body)
	}
}

// TestDeleteDocRequest pins DELETE /api/v1/docs/{id} and its body.
func TestDeleteDocRequest(t *testing.T) {
	c, got := deleteStub(t)

	doc, _, err := c.DeleteDoc(context.Background(), 12, "wrong corpus number")
	if err != nil {
		t.Fatalf("DeleteDoc: %v", err)
	}
	if got.Method != http.MethodDelete || got.URI != "/api/v1/docs/12" {
		t.Fatalf("DeleteDoc hit %s %s, want DELETE /api/v1/docs/12", got.Method, got.URI)
	}
	if got.Body != `{"justification":"wrong corpus number"}` {
		t.Fatalf("DeleteDoc body = %s", got.Body)
	}
	if doc.Tombstone == nil || doc.Tombstone.Justification != "seeded by mistake" {
		t.Fatalf("DeleteDoc decoded tombstone = %+v", doc.Tombstone)
	}

	if _, _, err := c.DeleteDoc(context.Background(), 12, ""); err != nil {
		t.Fatalf("DeleteDoc with no justification: %v", err)
	}
	if got.Body != `{}` {
		t.Fatalf("DeleteDoc body with no justification = %q, want {}", got.Body)
	}
}

// TestUndeleteDocRequest pins POST /api/v1/docs/{id}/undelete.
func TestUndeleteDocRequest(t *testing.T) {
	c, got := deleteStub(t)

	if _, _, err := c.UndeleteDoc(context.Background(), 12); err != nil {
		t.Fatalf("UndeleteDoc: %v", err)
	}
	if got.Method != http.MethodPost || got.URI != "/api/v1/docs/12/undelete" {
		t.Fatalf("UndeleteDoc hit %s %s, want POST /api/v1/docs/12/undelete", got.Method, got.URI)
	}
	if got.Body != "" {
		t.Fatalf("UndeleteDoc body = %q, want none", got.Body)
	}
}

// TestListFiltersDeletedSwitch pins that both list filters send deleted=true
// only when asked. The flag is a switch server-side (044 §5) — tombstoned rows
// replace the live ones — so an unset field must not leak the parameter.
func TestListFiltersDeletedSwitch(t *testing.T) {
	c, got := deleteStub(t)
	ctx := context.Background()

	if _, _, err := c.ListTasks(ctx, cli.TaskListFilter{Project: "wl", Deleted: true}); err != nil {
		t.Fatalf("ListTasks --deleted: %v", err)
	}
	if got.URI != "/api/v1/tasks?deleted=true&project=wl" {
		t.Fatalf("ListTasks --deleted URI = %q", got.URI)
	}
	if _, _, err := c.ListTasks(ctx, cli.TaskListFilter{Project: "wl"}); err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if got.URI != "/api/v1/tasks?project=wl" {
		t.Fatalf("ListTasks URI = %q, want no deleted parameter", got.URI)
	}

	if _, _, err := c.ListDocs(ctx, cli.DocListFilter{Project: "wl", Deleted: true}); err != nil {
		t.Fatalf("ListDocs --deleted: %v", err)
	}
	if got.URI != "/api/v1/docs?deleted=true&project=wl" {
		t.Fatalf("ListDocs --deleted URI = %q", got.URI)
	}
	if _, _, err := c.ListDocs(ctx, cli.DocListFilter{Project: "wl"}); err != nil {
		t.Fatalf("ListDocs: %v", err)
	}
	if got.URI != "/api/v1/docs?project=wl" {
		t.Fatalf("ListDocs URI = %q, want no deleted parameter", got.URI)
	}
}
