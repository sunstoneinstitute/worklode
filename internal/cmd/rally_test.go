package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestProjectRally covers `lode project rally <id>` (WL-667, L6): 404 with
// no open rally, then the rally's title and its one open blocker once
// `task add --kind rally` and `task block` assemble one, using no command
// this task did not add.
func TestProjectRally(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()

	if out, err := runLode(t, "project", "rally", "proj"); err == nil {
		t.Fatalf("rally with none open: want error\noutput: %s", out)
	}

	work := createTestTask(t, c, "the actual work")
	rally, _, err := c.CreateTask(ctx, model.CreateTaskInput{
		Project: "proj", Title: "finish the cockpit", Priority: "high", Kind: "rally",
	})
	if err != nil {
		t.Fatalf("create rally: %v", err)
	}
	if _, err := c.Block(ctx, rally.ID, work.ID); err != nil {
		t.Fatalf("block rally by work: %v", err)
	}

	out, err := runLode(t, "project", "rally", "proj")
	if err != nil {
		t.Fatalf("rally: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, rally.ID) || !strings.Contains(out, "finish the cockpit") {
		t.Fatalf("rally output = %q, want the rally's id and title", out)
	}
	if !strings.Contains(out, work.ID) || !strings.Contains(out, "the actual work") {
		t.Fatalf("rally output = %q, want its open blocker", out)
	}
}
