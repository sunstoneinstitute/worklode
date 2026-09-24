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
		{Rule: "WL-RULE-3", Heading: "Two", RuleVersion: 1, Current: 1, Source: "plan"},
		{Rule: "WL-RULE-7", Heading: "Seven", RuleVersion: 2, Current: 4, Source: "manual"},
		{Rule: "WL-RULE-9", Heading: "Nine", RuleVersion: 1, Current: 3, Source: "manual", Pinned: 1},
	}
	var b bytes.Buffer
	TaskDetailRender(&b, d, "")
	out := b.String()
	if !strings.Contains(out, "governed by: WL-RULE-3  Two (v1)") {
		t.Errorf("missing current link line:\n%s", out)
	}
	if !strings.Contains(out, "governed by: WL-RULE-7  Seven (v2, rule now v4)") {
		t.Errorf("missing moved-on link line:\n%s", out)
	}
	if !strings.Contains(out, "governed by: WL-RULE-9  Nine (v1, rule now v3, pinned v1)") {
		t.Errorf("missing pinned link line:\n%s", out)
	}
}

// TestTaskDetailRenderGovernedByResolvesTo: a withdrawn governing rule
// prints its live successors on the next line (R8); a live rule prints no
// such line.
func TestTaskDetailRenderGovernedByResolvesTo(t *testing.T) {
	d := model.TaskDetail{Task: model.Task{ID: "WL-12", Title: "T", Project: "worklode", Priority: "medium", Kind: "bug", State: "ready"}}
	d.GovernedBy = []model.TaskGovernance{
		{Rule: "WL-RULE-3", Heading: "Two", RuleVersion: 1, Current: 1, Source: "manual", Status: "withdrawn",
			ResolvesTo: []string{"WL-RULE-40", "WL-RULE-41"}},
		{Rule: "WL-RULE-7", Heading: "Seven", RuleVersion: 1, Current: 1, Source: "manual", Status: "accepted"},
	}
	var b bytes.Buffer
	TaskDetailRender(&b, d, "")
	out := b.String()
	if !strings.Contains(out, "governed by: WL-RULE-3  Two (v1)\n    -> WL-RULE-40, WL-RULE-41\n") {
		t.Errorf("missing resolves-to line:\n%s", out)
	}
	if strings.Contains(out, "governed by: WL-RULE-7  Seven (v1)\n    ->") {
		t.Errorf("live rule should print no resolves-to line:\n%s", out)
	}
}
