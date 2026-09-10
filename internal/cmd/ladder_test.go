package cmd

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// ladderTestSetup claims a task into a fresh worktree and creates a spec doc
// to name in --doc, and cd's the test into that worktree — the same
// resolveWorktreeTask entry point `lode task gap`/`lode task fix` use. It
// returns the client so callers can read task state back out of band.
func ladderTestSetup(t *testing.T) (model.Task, model.Doc, *cli.Client) {
	t.Helper()
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Ladder task")
	doc, _, err := c.CreateDoc(context.Background(), model.CreateDocInput{
		Project: "proj", Kind: "spec", Slug: "ladder-spec",
		Body: "---\nstatus: draft\n---\n\n# A spec\n",
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}

	root := initGitRepo(t)
	t.Chdir(root)
	if _, err := runLode(t, "work", "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode work next: %v", err)
	}
	dir := filepath.Join(root, worktree.DefaultBase, task.ID+"-ladder-task")
	t.Chdir(dir)

	return task, doc, c
}

// TestTaskGapCommandRecordsWithoutReleasingLease: `lode task gap` reports the
// gap and leaves the task's lease alone — unlike `lode task escalate`, this
// rung does not stop the executor (025 §15.5).
func TestTaskGapCommandRecordsWithoutReleasingLease(t *testing.T) {
	task, doc, c := ladderTestSetup(t)

	out, err := runLode(t, "task", "gap", doc.Slug, "--reason", "the spec skips the empty case")
	if err != nil {
		t.Fatalf("lode task gap: %v\noutput: %s", err, out)
	}

	detail, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.Lease == nil {
		t.Fatalf("task lease after gap = nil, want the lease still held")
	}
}

// TestTaskGapCommandRequiresReason: --reason is required, cobra refuses
// before any request goes out.
func TestTaskGapCommandRequiresReason(t *testing.T) {
	_, doc, _ := ladderTestSetup(t)

	if _, err := runLode(t, "task", "gap", doc.Slug); err == nil {
		t.Fatalf("lode task gap without --reason: err = nil, want error")
	}
}

// TestTaskFixCommandStartedAndFinished: `lode task fix` records both phases
// without releasing the lease.
func TestTaskFixCommandStartedAndFinished(t *testing.T) {
	task, doc, c := ladderTestSetup(t)

	out, err := runLode(t, "task", "fix", "--phase", "started", "--tier", "spec", "--doc", doc.Slug)
	if err != nil {
		t.Fatalf("lode task fix --phase started: %v\noutput: %s", err, out)
	}
	out, err = runLode(t, "task", "fix", "--phase", "finished", "--outcome", "resolved")
	if err != nil {
		t.Fatalf("lode task fix --phase finished: %v\noutput: %s", err, out)
	}

	detail, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.Lease == nil {
		t.Fatalf("task lease after fix = nil, want the lease still held")
	}
}

// TestTaskFixCommandRequiresPhase: --phase is required, cobra refuses before
// any request goes out.
func TestTaskFixCommandRequiresPhase(t *testing.T) {
	ladderTestSetup(t)

	if _, err := runLode(t, "task", "fix", "--tier", "spec"); err == nil {
		t.Fatalf("lode task fix without --phase: err = nil, want error")
	}
}
