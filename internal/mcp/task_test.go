package mcp_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestTaskTools(t *testing.T) {
	f := newFixture(t)
	cs := f.connect(t, f.alice, oldProtocol, nil)
	h := handleOf(t, call(t, cs, "project_use", map[string]any{"project": "WL", "confirmed": true}))
	api := f.client(f.alice)
	ctx := t.Context()

	blocker, _, err := api.CreateTask(ctx, model.CreateTaskInput{Project: "worklode", Title: "Blocker", Kind: "chore", Priority: "low"})
	if err != nil {
		t.Fatal(err)
	}

	// Filed through the tool: creator is the caller, kind alias resolved, edge written.
	res := call(t, cs, "task_add", map[string]any{
		"project": h, "kind": "bugs", "title": "Crash on save", "body": "It crashes.",
		"blocked_by": []string{"task " + strings.TrimPrefix(blocker.ID, "WL-")},
	})
	if res.IsError || !strings.HasPrefix(text(res, 0), "Filed WL-") {
		t.Fatalf("task_add = %+v", res.Content)
	}
	id := strings.TrimSuffix(strings.Fields(text(res, 0))[1], ",")
	got, _, err := api.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedBy != "alice" || got.Kind != "bug" {
		t.Fatalf("task = created_by %q kind %q", got.CreatedBy, got.Kind)
	}
	if len(got.Edges.Out) != 1 || got.Edges.Out[0].Type != "dependsOn" || got.Edges.Out[0].To != blocker.ID {
		t.Fatalf("edges = %+v", got.Edges)
	}

	// An alias word filters; a feature task stays out.
	if _, _, err := api.CreateTask(ctx, model.CreateTaskInput{Project: "worklode", Title: "New thing", Kind: "feature", Priority: "low"}); err != nil {
		t.Fatal(err)
	}
	res = call(t, cs, "task_list", map[string]any{"project": h, "kind": "bugs"})
	if res.IsError || !strings.Contains(text(res, 1), "Crash on save") || strings.Contains(text(res, 1), "New thing") {
		t.Fatalf("task_list bugs = %+v", res.Content)
	}

	// Update.
	res = call(t, cs, "task_update", map[string]any{"project": h, "id": id, "title": "Crash on load"})
	if res.IsError {
		t.Fatalf("task_update = %+v", res.Content)
	}
	if got, _, _ = api.GetTask(ctx, id); got.Title != "Crash on load" {
		t.Fatalf("title = %q", got.Title)
	}

	// A full ref needs no handle.
	if res = call(t, cs, "task_update", map[string]any{"id": id, "priority": "high"}); res.IsError {
		t.Fatalf("task_update without handle = %+v", res.Content)
	}

	// A refused transition is counted and reported as an error.
	res = call(t, cs, "task_set_state", map[string]any{"project": h, "id": id, "state": "released"})
	if !res.IsError {
		t.Fatalf("released from ready = %+v", res.Content)
	}
	// The API answers an illegal transition with 422, which the dispatcher counts as invalid.
	if n := f.count(t, "task_set_state", "invalid"); n != 1 {
		t.Fatalf("outcome counts = %v", n)
	}

	// Abandon, then reopen.
	if res = call(t, cs, "task_set_state", map[string]any{"project": h, "id": id, "state": "abandoned"}); res.IsError || text(res, 0) != id+" is now abandoned." {
		t.Fatalf("abandon = %+v", res.Content)
	}
	if res = call(t, cs, "task_set_state", map[string]any{"project": h, "id": id, "state": "ready"}); res.IsError || text(res, 0) != id+" is now ready." {
		t.Fatalf("reopen = %+v", res.Content)
	}
}
