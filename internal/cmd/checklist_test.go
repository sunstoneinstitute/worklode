package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestTaskChecklist covers `lode task checklist <id>` (the view) and
// `lode task set checklist <item> <true|false> <id>` (the write): checking
// by ordinal, unchecking by title, and the errors for an unknown item.
func TestTaskChecklist(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task, _, err := c.CreateTask(context.Background(), model.CreateTaskInput{
		Project: "proj", Title: "Checklist task", Priority: "high", Kind: "feature",
		Body: "intro\n- [ ] first item\n- [x] second item\n",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	out, err := runLode(t, "task", "checklist", task.ID)
	if err != nil {
		t.Fatalf("task checklist: %v", err)
	}
	if !strings.Contains(out, "first item") || !strings.Contains(out, "second item") {
		t.Fatalf("checklist view output %q missing item titles", out)
	}

	if _, err := runLode(t, "task", "set", "checklist", "0", "true", task.ID); err != nil {
		t.Fatalf("set checklist by ordinal: %v", err)
	}
	items, _, err := c.GetChecklist(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get checklist: %v", err)
	}
	if !items[0].Checked {
		t.Fatalf("item 0 not checked: %+v", items)
	}

	if _, err := runLode(t, "task", "set", "checklist", "second item", "false", task.ID); err != nil {
		t.Fatalf("set checklist by title: %v", err)
	}
	items, _, err = c.GetChecklist(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get checklist: %v", err)
	}
	if items[1].Checked {
		t.Fatalf("item 1 still checked: %+v", items)
	}

	if _, err := runLode(t, "task", "set", "checklist", "99", "true", task.ID); err == nil {
		t.Fatal("set checklist out-of-range ordinal: want error, got nil")
	}
	if _, err := runLode(t, "task", "set", "checklist", "0", "not-a-bool", task.ID); err == nil {
		t.Fatal("set checklist with non-bool value: want error, got nil")
	}
}
