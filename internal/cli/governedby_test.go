package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestTaskDetailRenderGovernedBy(t *testing.T) {
	d := model.TaskDetail{Task: model.Task{ID: "WL-12", Title: "T", Project: "worklode", Priority: "medium", Kind: "bug", State: "ready"}}
	d.GovernedBy = []model.TaskGovernance{
		{Clause: "WL-CL-3", Heading: "Two", ClauseVersion: 1, Current: 1, Source: "plan"},
		{Clause: "WL-CL-7", Heading: "Seven", ClauseVersion: 2, Current: 4, Source: "manual"},
		{Clause: "WL-CL-9", Heading: "Nine", ClauseVersion: 1, Current: 3, Source: "manual", Pinned: 1},
	}
	var b bytes.Buffer
	TaskDetailRender(&b, d, "")
	out := b.String()
	if !strings.Contains(out, "governed by: WL-CL-3  Two (v1)") {
		t.Errorf("missing current link line:\n%s", out)
	}
	if !strings.Contains(out, "governed by: WL-CL-7  Seven (v2, clause now v4)") {
		t.Errorf("missing moved-on link line:\n%s", out)
	}
	if !strings.Contains(out, "governed by: WL-CL-9  Nine (v1, clause now v3, pinned v1)") {
		t.Errorf("missing pinned link line:\n%s", out)
	}
}

// TestTaskDetailRenderGovernedByResolvesTo: a withdrawn governing clause
// prints its live successors on the next line (R8); a live clause prints no
// such line.
func TestTaskDetailRenderGovernedByResolvesTo(t *testing.T) {
	d := model.TaskDetail{Task: model.Task{ID: "WL-12", Title: "T", Project: "worklode", Priority: "medium", Kind: "bug", State: "ready"}}
	d.GovernedBy = []model.TaskGovernance{
		{Clause: "WL-CL-3", Heading: "Two", ClauseVersion: 1, Current: 1, Source: "manual", Status: "withdrawn",
			ResolvesTo: []string{"WL-CL-40", "WL-CL-41"}},
		{Clause: "WL-CL-7", Heading: "Seven", ClauseVersion: 1, Current: 1, Source: "manual", Status: "accepted"},
	}
	var b bytes.Buffer
	TaskDetailRender(&b, d, "")
	out := b.String()
	if !strings.Contains(out, "governed by: WL-CL-3  Two (v1)\n    -> WL-CL-40, WL-CL-41\n") {
		t.Errorf("missing resolves-to line:\n%s", out)
	}
	if strings.Contains(out, "governed by: WL-CL-7  Seven (v1)\n    ->") {
		t.Errorf("live clause should print no resolves-to line:\n%s", out)
	}
}
