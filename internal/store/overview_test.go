// Store reads for the server-side drift derivers and the read-only overview
// frontier (docs/plans/2026-07-30-drift-and-overview-2-server-derivers.md,
// task 7). These compose the same fixture helpers tasks_test.go,
// changes_test.go, artifacts_test.go and delivery_test.go already use.

package store

import (
	"context"
	"testing"
)

func TestTaskPRs(t *testing.T) {
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	// Bound: head ref carries the task id, so UpsertPR correlates it.
	if _, err := upsertPR(t, s, defaultPR(task.ID), ""); err != nil {
		t.Fatalf("upsertPR bound: %v", err)
	}
	// Unbound: head ref matches no task, and there is no body trailer either.
	unbound := defaultPR(task.ID)
	unbound.Number = 2
	unbound.HeadRef = "unrelated-branch"
	if _, err := upsertPR(t, s, unbound, ""); err != nil {
		t.Fatalf("upsertPR unbound: %v", err)
	}

	prs, err := s.TaskPRs(context.Background())
	if err != nil {
		t.Fatalf("TaskPRs: %v", err)
	}
	if len(prs) != 1 || prs[0].Repo != "sunstoneinstitute/demo" || prs[0].Number != 1 || prs[0].TaskID != task.ID {
		t.Fatalf("TaskPRs = %+v; want the one task-bound PR", prs)
	}
}

func TestAllBlockEdges(t *testing.T) {
	s := openTaskStore(t)
	a := createTask(t, s, taskTestNow, defaultTaskInput())
	b := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, a.ID, b.ID, "blocks"); err != nil {
		t.Fatalf("addEdge blocks: %v", err)
	}
	if err := addEdge(t, s, b.ID, a.ID, "child_of"); err != nil {
		t.Fatalf("addEdge child_of: %v", err)
	}

	edges, err := s.AllBlockEdges(context.Background())
	if err != nil {
		t.Fatalf("AllBlockEdges: %v", err)
	}
	if len(edges) != 1 || edges[0].FromTask != a.ID || edges[0].ToTask != b.ID || edges[0].Type != "blocks" {
		t.Fatalf("AllBlockEdges = %+v; want exactly %s blocks %s", edges, a.ID, b.ID)
	}
}

func TestFrontierMirrorsClaimNextOrder(t *testing.T) {
	s := openClaimNextStore(t)
	ctx := context.Background()

	lowIn := defaultTaskInput()
	lowIn.Priority = "low"
	low := createTask(t, s, claimNextTestNow, lowIn)

	critIn := defaultTaskInput()
	critIn.Priority = "critical"
	crit := createTask(t, s, claimNextTestNow, critIn)

	// Give low a blocking fan-out so the fan-out map has something to assert on.
	filler := createTask(t, s, claimNextTestNow, defaultTaskInput())
	if err := addEdge(t, s, low.ID, filler.ID, "blocks"); err != nil {
		t.Fatalf("addEdge: %v", err)
	}

	tasks, fanOut, err := s.Frontier(ctx, "")
	if err != nil {
		t.Fatalf("Frontier: %v", err)
	}
	if len(tasks) != 2 || tasks[0].ID != crit.ID {
		t.Fatalf("Frontier order = %v; critical priority must sort first", taskIDs(tasks))
	}
	if fanOut == nil {
		t.Fatalf("Frontier fan-out map is nil")
	}
	if fanOut[low.ID] != 1 {
		t.Fatalf("Frontier fan-out[%s] = %d, want 1", low.ID, fanOut[low.ID])
	}

	// The mirror contract: Frontier's head equals ClaimNext's dry-run pick.
	res, err := s.ClaimNext(ctx, ClaimNextOpts{DryRun: true})
	if err != nil || res.Task == nil {
		t.Fatalf("ClaimNext dry run: %+v, %v", res, err)
	}
	if res.Task.ID != tasks[0].ID {
		t.Fatalf("frontier head %s != claim-next pick %s", tasks[0].ID, res.Task.ID)
	}
}

func TestAllArtifactsByID(t *testing.T) {
	s := openArtifactsStore(t)
	a := defaultArtifact()

	id, err := createArtifact(t, s, a)
	if err != nil {
		t.Fatalf("createArtifact: %v", err)
	}

	byID, err := s.AllArtifactsByID(context.Background())
	if err != nil {
		t.Fatalf("AllArtifactsByID: %v", err)
	}
	got, ok := byID[id]
	if !ok {
		t.Fatalf("AllArtifactsByID missing id %d: %v", id, byID)
	}
	if got.Kind != a.Kind || got.Name != a.Name || got.Version != a.Version || got.Repo != a.Repo || got.SourceSHA != a.SourceSHA {
		t.Fatalf("AllArtifactsByID[%d] = %+v; want round-trip of %+v", id, got, a)
	}
}

func TestAllReleaseFrontiersAndHasMainCommit(t *testing.T) {
	s := openArtifactsStore(t)

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	mainID, err := AppendMainCommit(tx, "acme/app", "deadbeef1", artifactsTestNow)
	if err != nil {
		t.Fatalf("AppendMainCommit: %v", err)
	}
	if err := SetReleaseFrontier(tx, "acme/app", "v1.0.0", mainID, artifactsTestNow); err != nil {
		t.Fatalf("SetReleaseFrontier: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	frontiers, err := s.AllReleaseFrontiers(context.Background())
	if err != nil {
		t.Fatalf("AllReleaseFrontiers: %v", err)
	}
	if len(frontiers) != 1 || frontiers[0].Repo != "acme/app" || frontiers[0].Tag != "v1.0.0" || frontiers[0].SHA != "deadbeef1" {
		t.Fatalf("AllReleaseFrontiers = %+v; want one row for acme/app v1.0.0 @ deadbeef1", frontiers)
	}

	ok, err := s.HasMainCommit(context.Background(), "acme/app", "deadbeef1")
	if err != nil || !ok {
		t.Fatalf("HasMainCommit(known) = %v, %v; want true, nil", ok, err)
	}
	ok, err = s.HasMainCommit(context.Background(), "acme/app", "unknown-sha")
	if err != nil || ok {
		t.Fatalf("HasMainCommit(unknown) = %v, %v; want false, nil", ok, err)
	}
}
