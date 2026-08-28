//go:build e2e

// delete_test.go proves spec 044 end to end through public surfaces only: a
// task and a document are deleted and undeleted over the HTTP API, on a prod
// instance (where a justification is required) and on a dev one (where it is
// not). Nothing here writes to the store directly.
package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// deleteSpecBody is a minimal well-formed spec: frontmatter, an H1, and one
// anchored section whose anchor agrees with its number.
const deleteSpecBody = `---
status: draft
---

# Deletable Spec

## 1. Scope {#sec-1}

Scope body.
`

// deleteFixture stands up a server at the given instance environment with one
// project, one actor, and that actor's client.
func deleteFixture(t *testing.T, instanceEnv string) (*cli.Client, func()) {
	t.Helper()
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		InstanceEnv:    instanceEnv,
	})
	if err != nil {
		t.Fatalf("new server (%s): %v", instanceEnv, err)
	}
	srv := httptest.NewServer(handler)

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "del", Name: "Delete E2E", Key: "DEL",
	}); err != nil {
		srv.Close()
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "deleter", Kind: "human", DisplayName: "deleter",
	}); err != nil {
		srv.Close()
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "deleter", "e2e delete", nil)
	if err != nil {
		srv.Close()
		t.Fatalf("create token: %v", err)
	}
	return cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token}), srv.Close
}

// taskIDs reads the ids out of a task list, for the set assertions below.
func taskIDs(resp model.TaskListResponse) []string {
	ids := make([]string, 0, len(resp.Tasks))
	for _, t := range resp.Tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

// TestDeleteOnProdInstanceRequiresJustification walks the whole prod path:
// the delete without a reason is refused, the one with a reason tombstones
// the task, the task leaves every list, an id-addressed read still finds it
// and renders the tombstone, and undelete brings it back.
func TestDeleteOnProdInstanceRequiresJustification(t *testing.T) {
	ctx := context.Background()
	c, done := deleteFixture(t, api.InstanceProd)
	defer done()

	task, _, err := c.CreateTask(ctx, model.CreateTaskInput{
		Project: "del", Title: "Delete me", Priority: "medium", Kind: "chore",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// 1. A prod instance refuses a delete carrying no justification, and says
	// so as a validation failure rather than a malformed request (044 §5).
	if _, _, err := c.DeleteTask(ctx, task.ID, ""); err == nil {
		t.Fatal("delete without justification on prod: want an error, got nil")
	} else if status := clientErrStatus(t, err); status != http.StatusUnprocessableEntity {
		t.Fatalf("delete without justification: status = %d, want 422 (err %v)", status, err)
	}
	// Whitespace is not a justification.
	if _, _, err := c.DeleteTask(ctx, task.ID, "   "); err == nil {
		t.Fatal("delete with blank justification on prod: want an error, got nil")
	}

	// 2. With a reason it goes through, and the response carries the whole
	// tombstone: who, when, why.
	deleted, _, err := c.DeleteTask(ctx, task.ID, "seeded by mistake")
	if err != nil {
		t.Fatalf("delete task: %v", err)
	}
	if deleted.Tombstone == nil {
		t.Fatal("deleted task carries no tombstone")
	}
	if deleted.Tombstone.Justification != "seeded by mistake" {
		t.Fatalf("tombstone justification = %q, want %q",
			deleted.Tombstone.Justification, "seeded by mistake")
	}
	if deleted.Tombstone.DeletedBy != "deleter" {
		t.Fatalf("tombstone deleted_by = %q, want deleter", deleted.Tombstone.DeletedBy)
	}
	if deleted.Tombstone.DeletedAt.IsZero() {
		t.Fatal("tombstone deleted_at is zero")
	}

	// 3. Deleting it again is refused: the caller is hiding a row that is
	// already hidden, and would overwrite someone else's tombstone.
	if _, _, err := c.DeleteTask(ctx, task.ID, "again"); err == nil {
		t.Fatal("second delete: want an error, got nil")
	} else if status := clientErrStatus(t, err); status != http.StatusUnprocessableEntity {
		t.Fatalf("second delete: status = %d, want 422 (err %v)", status, err)
	}

	// 4. It is out of the default list and is the whole of the deleted list
	// (044 §4, §5 — --deleted is a switch, not an addition).
	live, _, err := c.ListTasks(ctx, cli.TaskListFilter{Project: "del"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if ids := taskIDs(live); len(ids) != 0 {
		t.Fatalf("live task list = %v, want empty", ids)
	}
	tombstoned, _, err := c.ListTasks(ctx, cli.TaskListFilter{Project: "del", Deleted: true})
	if err != nil {
		t.Fatalf("list deleted tasks: %v", err)
	}
	if ids := taskIDs(tombstoned); len(ids) != 1 || ids[0] != task.ID {
		t.Fatalf("deleted task list = %v, want [%s]", ids, task.ID)
	}

	// 5. Reading it by id still works and shows the tombstone: an id an agent
	// already holds must not report "not found" when the truth is "deleted,
	// by this person, for this reason" (044 §4).
	detail, _, err := c.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get deleted task: %v", err)
	}
	if detail.Task.Tombstone == nil || detail.Task.Tombstone.Justification != "seeded by mistake" {
		t.Fatalf("get deleted task: tombstone = %+v, want the delete record", detail.Task.Tombstone)
	}

	// 6. Undelete needs no justification on either instance, and puts the
	// task back in the live list.
	restored, _, err := c.UndeleteTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("undelete task: %v", err)
	}
	if restored.Tombstone != nil {
		t.Fatalf("undeleted task still carries a tombstone: %+v", restored.Tombstone)
	}
	live, _, err = c.ListTasks(ctx, cli.TaskListFilter{Project: "del"})
	if err != nil {
		t.Fatalf("list tasks after undelete: %v", err)
	}
	if ids := taskIDs(live); len(ids) != 1 || ids[0] != task.ID {
		t.Fatalf("live task list after undelete = %v, want [%s]", ids, task.ID)
	}

	// 7. Undeleting a live task is refused for the mirror-image reason.
	if _, _, err := c.UndeleteTask(ctx, task.ID); err == nil {
		t.Fatal("undelete of a live task: want an error, got nil")
	}
}

// TestDeleteOnDevInstanceNeedsNoJustification is the same act on a dev
// instance, where the row is noise and nobody should be made to explain it
// (044 §3). The tombstone is otherwise identical.
func TestDeleteOnDevInstanceNeedsNoJustification(t *testing.T) {
	ctx := context.Background()
	c, done := deleteFixture(t, api.InstanceDev)
	defer done()

	task, _, err := c.CreateTask(ctx, model.CreateTaskInput{
		Project: "del", Title: "Re-seeded", Priority: "medium", Kind: "chore",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	deleted, _, err := c.DeleteTask(ctx, task.ID, "")
	if err != nil {
		t.Fatalf("delete task on dev: %v", err)
	}
	if deleted.Tombstone == nil {
		t.Fatal("deleted task carries no tombstone")
	}
	if deleted.Tombstone.Justification != "" {
		t.Fatalf("tombstone justification = %q, want empty", deleted.Tombstone.Justification)
	}
	if deleted.Tombstone.DeletedBy != "deleter" || deleted.Tombstone.DeletedAt.IsZero() {
		t.Fatalf("dev tombstone is not fully stamped: %+v", deleted.Tombstone)
	}

	// A justification given on dev is stored exactly as one given on prod:
	// the environment gates the demand, not the mechanism (044 §3).
	other, _, err := c.CreateTask(ctx, model.CreateTaskInput{
		Project: "del", Title: "Also re-seeded", Priority: "medium", Kind: "chore",
	})
	if err != nil {
		t.Fatalf("create second task: %v", err)
	}
	withReason, _, err := c.DeleteTask(ctx, other.ID, "duplicate import")
	if err != nil {
		t.Fatalf("delete second task on dev: %v", err)
	}
	if withReason.Tombstone == nil || withReason.Tombstone.Justification != "duplicate import" {
		t.Fatalf("dev tombstone = %+v, want justification %q", withReason.Tombstone, "duplicate import")
	}
}

// TestDeleteDocument is the document half of 044: same tombstone, same
// instance rule, same list behaviour.
func TestDeleteDocument(t *testing.T) {
	ctx := context.Background()
	c, done := deleteFixture(t, api.InstanceProd)
	defer done()

	doc, _, err := c.CreateDoc(ctx, model.CreateDocInput{
		Project: "del", Kind: "spec", Number: 1, Slug: "deletable-spec",
		Body: deleteSpecBody, Owner: "deleter",
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}

	if _, _, err := c.DeleteDoc(ctx, doc.ID, ""); err == nil {
		t.Fatal("delete doc without justification on prod: want an error, got nil")
	} else if status := clientErrStatus(t, err); status != http.StatusUnprocessableEntity {
		t.Fatalf("delete doc without justification: status = %d, want 422 (err %v)", status, err)
	}

	deleted, _, err := c.DeleteDoc(ctx, doc.ID, "wrong corpus number")
	if err != nil {
		t.Fatalf("delete doc: %v", err)
	}
	if deleted.Tombstone == nil || deleted.Tombstone.Justification != "wrong corpus number" {
		t.Fatalf("doc tombstone = %+v, want the delete record", deleted.Tombstone)
	}

	live, _, err := c.ListDocs(ctx, cli.DocListFilter{Project: "del"})
	if err != nil {
		t.Fatalf("list docs: %v", err)
	}
	if len(live.Docs) != 0 {
		t.Fatalf("live doc list has %d docs, want 0", len(live.Docs))
	}
	tombstoned, _, err := c.ListDocs(ctx, cli.DocListFilter{Project: "del", Deleted: true})
	if err != nil {
		t.Fatalf("list deleted docs: %v", err)
	}
	if len(tombstoned.Docs) != 1 || tombstoned.Docs[0].ID != doc.ID {
		t.Fatalf("deleted doc list = %+v, want just doc %d", tombstoned.Docs, doc.ID)
	}

	got, _, err := c.GetDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get deleted doc: %v", err)
	}
	if got.Doc.Tombstone == nil {
		t.Fatal("get deleted doc: tombstone missing")
	}

	if _, _, err := c.UndeleteDoc(ctx, doc.ID); err != nil {
		t.Fatalf("undelete doc: %v", err)
	}
	live, _, err = c.ListDocs(ctx, cli.DocListFilter{Project: "del"})
	if err != nil {
		t.Fatalf("list docs after undelete: %v", err)
	}
	if len(live.Docs) != 1 {
		t.Fatalf("live doc list after undelete has %d docs, want 1", len(live.Docs))
	}
}
