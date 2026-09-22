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
