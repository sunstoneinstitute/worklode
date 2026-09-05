package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestRallyRender checks the rally's own id/title print first, then its
// members via BlockerTreeRender — reused, not reimplemented.
func TestRallyRender(t *testing.T) {
	var buf bytes.Buffer
	cli.RallyRender(&buf, model.Rally{
		Task: model.Task{ID: "WL-9", Title: "finish the cockpit"},
		Blockers: model.BlockerTree{
			Root: "WL-9",
			Blockers: []model.BlockerNode{
				{ID: "WL-2", Title: "the work", State: "ready", Via: "WL-9", Depth: 1},
			},
		},
	})
	got := buf.String()

	want := []string{
		"WL-9  finish the cockpit",
		"WL-9 is blocked by:",
		"  WL-2  the work  (ready)",
	}
	for _, line := range want {
		if !strings.Contains(got, line+"\n") {
			t.Fatalf("missing line %q in:\n%s", line, got)
		}
	}

	buf.Reset()
	cli.RallyRender(&buf, model.Rally{
		Task:     model.Task{ID: "WL-9", Title: "finish the cockpit"},
		Blockers: model.BlockerTree{Root: "WL-9"},
	})
	if got := buf.String(); got != "WL-9  finish the cockpit\nnothing is blocking WL-9\n" {
		t.Fatalf("empty rally = %q", got)
	}
}
