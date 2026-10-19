package mcp_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestDocTools(t *testing.T) {
	f := newFixture(t)
	cs := f.connect(t, f.alice, oldProtocol, nil)
	h := handleOf(t, call(t, cs, "project_use", map[string]any{"project": "WL", "confirmed": true}))
	api := f.client(f.alice)
	ctx := t.Context()

	// An alias word files a plan; the title becomes the heading.
	res := call(t, cs, "doc_add", map[string]any{"project": h, "kind": "implementation plan", "title": "Doc tools plan", "body": "## 1. Steps {#sec-1}\n\nDo it.\n"})
	if res.IsError || !strings.HasPrefix(text(res, 0), "Created the draft plan") {
		t.Fatalf("doc_add = %+v", res.Content)
	}
	plan, err := api.ResolveDoc(ctx, "doc-tools-plan")
	if err != nil || plan.Title != "Doc tools plan" || plan.Status != "draft" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}

	// A rule kind is not a document kind.
	if res = call(t, cs, "doc_add", map[string]any{"project": h, "kind": "invariant", "title": "x", "body": "y"}); !res.IsError {
		t.Fatalf("doc_add invariant = %+v", res.Content)
	}

	// Edit with the version just read, then list it.
	body := plan.Body + "\nMore.\n"
	res = call(t, cs, "doc_edit", map[string]any{"project": h, "id": "doc-tools-plan", "body": body, "if_version": plan.Version})
	if res.IsError || !strings.HasPrefix(text(res, 0), "Updated ") {
		t.Fatalf("doc_edit = %+v", res.Content)
	}
	res = call(t, cs, "doc_list", map[string]any{"project": h, "kind": "plans", "status": "draft"})
	if res.IsError || !strings.Contains(text(res, 1), "Doc tools plan") {
		t.Fatalf("doc_list = %+v", res.Content)
	}

	// A version the document has moved past is a conflict that names the current one.
	call(t, cs, "doc_add", map[string]any{"project": h, "kind": "spec", "title": "Stale thing", "body": "## 1. Scope {#sec-1}\n\nText.\n"})
	spec, err := api.ResolveDoc(ctx, "stale-thing")
	if err != nil {
		t.Fatal(err)
	}
	acc, _, err := api.AcceptDoc(ctx, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec = acc.Doc
	for i, want := range []bool{false, true} {
		res = call(t, cs, "doc_edit", map[string]any{"project": h, "id": "stale-thing", "patch": spec.Body + "\nTypo fix.\n", "note": "Fix a typo.", "if_version": spec.Version})
		if res.IsError != want {
			t.Fatalf("patch %d = %q", i, text(res, 0))
		}
	}
	if !strings.Contains(text(res, 0), "current version is 2") {
		t.Fatalf("stale patch = %q", text(res, 0))
	}
	if n := f.count(t, "doc_edit", "conflict"); n != 1 {
		t.Fatalf("conflict count = %v", n)
	}
}

func TestDocTransitions(t *testing.T) {
	f := newFixture(t)
	alice := f.connect(t, f.alice, oldProtocol, nil)
	bob := f.connect(t, f.bob, oldProtocol, nil)
	h := handleOf(t, call(t, alice, "project_use", map[string]any{"project": "WL", "confirmed": true}))
	hb := handleOf(t, call(t, bob, "project_use", map[string]any{"project": "WL", "confirmed": true}))
	api := f.client(f.alice)
	ctx := t.Context()

	call(t, alice, "doc_add", map[string]any{"project": h, "kind": "spec", "title": "Reviewed thing", "body": "## 1. Scope {#sec-1}\n\nText.\n"})
	// Submitting goes through the lifecycle, which mints the review task.
	if res := call(t, alice, "doc_transition", map[string]any{"project": h, "id": "reviewed-thing", "transition": "submit"}); res.IsError {
		t.Fatalf("submit = %+v", res.Content)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		l, _, err := api.ListTasks(ctx, model.TaskListParams{Project: "worklode", Kind: "review"})
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Tasks) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no review task minted after submit")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Bob does not own the document, so the owner gate refuses him.
	res := call(t, bob, "doc_transition", map[string]any{"project": hb, "id": "reviewed-thing", "transition": "accept"})
	if !res.IsError {
		t.Fatalf("bob accept = %+v", res.Content)
	}
	if n := f.count(t, "doc_transition", "refused"); n != 1 {
		t.Fatalf("refused count = %v", n)
	}
	if res = call(t, alice, "doc_transition", map[string]any{"project": h, "id": "reviewed-thing", "transition": "bogus"}); !res.IsError {
		t.Fatalf("bogus = %+v", res.Content)
	}
}
