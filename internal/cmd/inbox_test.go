package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// TestInboxPromoteResolvesBareParentNumber mirrors the --under handling
// covered by TestTaskHierarchyCommands in task_test.go: lode inbox promote
// must run its --parent through the same resolveTaskID as every other
// task-id flag, so a bare number is accepted there too, not just in
// lode inbox link.
func TestInboxPromoteResolvesBareParentNumber(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/widgets")
	seedIssue(t, st, "acme/widgets", 1)

	container, _, err := c.CreateTask(context.Background(), cli.CreateTaskInput{
		Project: "proj", Title: "Container", Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	setupRepoConfig(t, "proj")

	parentNumber := container.ID[strings.LastIndex(container.ID, "-")+1:]
	out, err := runLode(t, "inbox", "promote", "acme/widgets", "1", "--json",
		"--priority", "low", "--parent", parentNumber)
	if err != nil {
		t.Fatalf("inbox promote --parent %s: %v\noutput: %s", parentNumber, err, out)
	}
	var task cli.Task
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}

	parent, err := st.ParentOf(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("parent of %s: %v", task.ID, err)
	}
	if parent == nil || parent.ID != container.ID {
		t.Fatalf("parent = %v, want %s", parent, container.ID)
	}
}
