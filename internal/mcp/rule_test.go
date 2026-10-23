package mcp_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestRuleTools(t *testing.T) {
	f := newFixture(t)
	cs := f.connect(t, f.alice, oldProtocol, nil)
	h := handleOf(t, call(t, cs, "project_use", map[string]any{"project": "WL", "confirmed": true}))
	api := f.client(f.alice)
	ctx := t.Context()

	add := func(heading, kind string) string {
		t.Helper()
		res := call(t, cs, "rule_add", map[string]any{"project": h, "heading": heading, "body": "Body of " + heading + ".", "kind": kind})
		if res.IsError || !strings.HasPrefix(text(res, 0), "Added the draft") {
			t.Fatalf("rule_add = %+v", res.Content)
		}
		return strings.TrimSuffix(strings.Fields(text(res, 0))[4], ":")
	}
	a := add("First rule", "")
	b := add("Second rule", "invariants")
	c := add("Third rule", "principle")
	if got, _, err := api.GetRule(ctx, b); err != nil || got.Kind != "invariant" || got.Status != "draft" {
		t.Fatalf("added = %+v, %v", got, err)
	}
	if res := call(t, cs, "rule_add", map[string]any{"project": h, "heading": "x", "body": "y", "kind": "bug"}); !res.IsError {
		t.Fatalf("rule_add bug = %+v", res.Content)
	}

	res := call(t, cs, "rule_list", map[string]any{"project": h})
	if res.IsError || !strings.Contains(text(res, 1), "Second rule") {
		t.Fatalf("rule_list = %+v", res.Content)
	}

	// Edit keeps the heading when only the body is given.
	res = call(t, cs, "rule_edit", map[string]any{"project": h, "id": a, "body": "New body."})
	if res.IsError {
		t.Fatalf("rule_edit = %+v", res.Content)
	}
	if got, _, _ := api.GetRule(ctx, a); got.Heading != "First rule" || !strings.Contains(string(got.Body), "New body.") {
		t.Fatalf("edited = %+v", got)
	}

	// Link, then unlink.
	for _, remove := range []bool{false, true} {
		want := !remove
		res = call(t, cs, "rule_link", map[string]any{"project": h, "id": a, "rel": "needs", "target": b, "remove": remove})
		if res.IsError {
			t.Fatalf("rule_link remove=%v: %q", remove, text(res, 0))
		}
		got, _, _ := api.GetRule(ctx, a)
		linked := slices.ContainsFunc(got.Edges, func(e model.RuleEdge) bool { return e.Type == "needs" && e.From == a && e.To == b })
		if linked != want {
			t.Fatalf("after remove=%v, linked = %v", remove, linked)
		}
	}
	if res = call(t, cs, "rule_link", map[string]any{"project": h, "id": a, "rel": "bogus", "target": b}); !res.IsError {
		t.Fatalf("bogus rel = %+v", res.Content)
	}
	if n := f.count(t, "rule_link", "invalid"); n != 1 {
		t.Fatalf("invalid count = %v", n)
	}

	// Supersede withdraws the old rule.
	res = call(t, cs, "rule_supersede", map[string]any{"project": h, "map": b + " -> " + c + "\n"})
	if res.IsError {
		t.Fatalf("rule_supersede = %+v", res.Content)
	}
	if got, _, _ := api.GetRule(ctx, b); got.Status == "draft" {
		t.Fatalf("superseded rule = %+v", got)
	}
}
