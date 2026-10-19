package mcp_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestFileTool(t *testing.T) {
	f := newFixture(t)
	cs := f.connect(t, f.alice, oldProtocol, nil)
	h := handleOf(t, call(t, cs, "project_use", map[string]any{"project": "WL", "confirmed": true}))
	api := f.client(f.alice)
	ctx := t.Context()
	file := func(what, title string, extra map[string]any) (string, bool) {
		args := map[string]any{"project": h, "what": what, "title": title, "body": "Body."}
		for k, v := range extra {
			args[k] = v
		}
		res := call(t, cs, "file", args)
		return text(res, 0), res.IsError
	}

	out, isErr := file("bug", "Crash on save", nil)
	if isErr || !strings.HasPrefix(out, "Filed WL-") || !strings.Contains(out, "a bug task") {
		t.Fatalf("bug = %q", out)
	}
	if out, isErr = file("spec", "Cache design", nil); isErr || !strings.HasPrefix(out, "Created the draft spec") {
		t.Fatalf("spec = %q", out)
	}
	before, _, _ := api.ListTasks(ctx, model.TaskListParams{Project: "worklode"})
	if out, isErr = file("design", "Anything", nil); !isErr || !strings.Contains(out, "spec") || !strings.Contains(out, "design task") {
		t.Fatalf("design = %q", out)
	}
	if out, isErr = file("bgu", "Anything", nil); !isErr || !strings.Contains(out, "bug") {
		t.Fatalf("bgu = %q", out)
	}
	after, _, _ := api.ListTasks(ctx, model.TaskListParams{Project: "worklode"})
	if len(after.Tasks) != len(before.Tasks) {
		t.Fatalf("refused file created tasks: %d -> %d", len(before.Tasks), len(after.Tasks))
	}

	// A review about a document names it; a rule about a document is arranged in it.
	doc, _ := file("spec", "Review target", nil)
	ref := strings.TrimSuffix(strings.Fields(doc)[4], ":")
	if out, isErr = file("review", "Look at it", map[string]any{"doc": ref}); isErr || !strings.Contains(out, "a review task") {
		t.Fatalf("review = %q", out)
	}
	if out, isErr = file("invariant", "Always true", map[string]any{"doc": ref}); isErr || !strings.HasPrefix(out, "Added the draft invariant") {
		t.Fatalf("rule = %q", out)
	}
}
