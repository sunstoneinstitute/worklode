package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestBlockerTreeRender checks the two things the flat-to-nested rebuild can
// get wrong: a deeper node must indent under its Via and not under the root,
// and a node marked Cycle must not be expanded (its child is itself, so
// expanding it never terminates).
func TestBlockerTreeRender(t *testing.T) {
	var buf bytes.Buffer
	cli.BlockerTreeRender(&buf, model.BlockerTree{
		Root: "WL-1",
		Blockers: []model.BlockerNode{
			{ID: "WL-2", Title: "near", State: "ready", Via: "WL-1", Depth: 1},
			{ID: "WL-3", Title: "far", State: "blocked", Via: "WL-2", Depth: 2},
			{ID: "WL-2", Title: "near", State: "ready", Via: "WL-3", Depth: 3, Cycle: true},
		},
		BlockingPlans: []model.DocRef{{Slug: "a-plan", Title: "A plan", Status: "draft"}},
	})
	got := buf.String()

	want := []string{
		"WL-1 is blocked by:",
		"  WL-2  near  (ready)",
		"    WL-3  far  (blocked)",
		"      WL-2  near  (ready)  (cycle)",
		"and by plans:",
		"  a-plan  A plan  (draft)",
	}
	if lines := strings.Split(strings.TrimRight(got, "\n"), "\n"); len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), got)
	}
	for _, line := range want {
		if !strings.Contains(got, line+"\n") {
			t.Fatalf("missing line %q in:\n%s", line, got)
		}
	}

	buf.Reset()
	cli.BlockerTreeRender(&buf, model.BlockerTree{Root: "WL-9"})
	if got := buf.String(); got != "nothing is blocking WL-9\n" {
		t.Fatalf("empty tree = %q", got)
	}
}

func TestBlockerForestRender(t *testing.T) {
	var buf bytes.Buffer
	cli.BlockerForestRender(&buf, model.BlockerForest{Trees: []model.BlockerTree{
		{Root: "WL-1", Blockers: []model.BlockerNode{
			{ID: "WL-2", Title: "one", State: "ready", Via: "WL-1", Depth: 1}}},
		{Root: "WL-5", Blockers: []model.BlockerNode{
			{ID: "WL-6", Title: "two", State: "ready", Via: "WL-5", Depth: 1}}},
	}})
	want := "WL-1 is blocked by:\n  WL-2  one  (ready)\n\nWL-5 is blocked by:\n  WL-6  two  (ready)\n"
	if got := buf.String(); got != want {
		t.Fatalf("forest =\n%q\nwant\n%q", got, want)
	}

	buf.Reset()
	cli.BlockerForestRender(&buf, model.BlockerForest{})
	if got := buf.String(); got != "nothing is blocked\n" {
		t.Fatalf("empty forest = %q", got)
	}
}
