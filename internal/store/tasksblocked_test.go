package store

import (
	"slices"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestBlocksEdgeAndBlockedTaskIDs(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()

	blocker := createTask(t, s, taskTestNow, defaultTaskInput()) // HDB-1
	blocked := createTask(t, s, taskTestNow, defaultTaskInput()) // HDB-2

	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}

	// Blocked while the blocker is ready.
	ids, err := s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if !ids[blocked.ID] || ids[blocker.ID] {
		t.Fatalf("BlockedTaskIDs with blocker ready: got %v, want only %s", ids, blocked.ID)
	}
	if !isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked(%s): want true while blocker ready", blocked.ID)
	}

	// Still blocked while the blocker is in_progress.
	walkTo(t, s, blocker.ID, "in_progress")
	ids, err = s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if !ids[blocked.ID] {
		t.Fatalf("BlockedTaskIDs with blocker in_progress: %s missing from %v", blocked.ID, ids)
	}

	// Unblocked once the blocker is merged (legal walk: in_review then merged).
	if err := transition(t, s, taskTestNow, blocker.ID, "in_progress", "in_review"); err != nil {
		t.Fatalf("transition to in_review: %v", err)
	}
	if err := transition(t, s, taskTestNow, blocker.ID, "in_review", "merged"); err != nil {
		t.Fatalf("transition to merged: %v", err)
	}
	ids, err = s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if ids[blocked.ID] {
		t.Fatalf("BlockedTaskIDs with blocker merged: %s should be unblocked, got %v", blocked.ID, ids)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked(%s): want false after blocker merged", blocked.ID)
	}
}

// TestIsTaskBlockedAgreesWithBlockedTaskIDs pins the single-task reader the
// task page uses against the map form the lists use. Both render the same
// "blocked" chip, so a page and a list must never disagree about one task.
func TestIsTaskBlockedAgreesWithBlockedTaskIDs(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()

	blocker := createTask(t, s, taskTestNow, defaultTaskInput()) // HDB-1
	blocked := createTask(t, s, taskTestNow, defaultTaskInput()) // HDB-2
	loose := createTask(t, s, taskTestNow, defaultTaskInput())   // HDB-3

	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}

	agree := func(when string) map[string]bool {
		t.Helper()
		ids, err := s.BlockedTaskIDs(ctx)
		if err != nil {
			t.Fatalf("BlockedTaskIDs %s: %v", when, err)
		}
		for _, id := range []string{blocker.ID, blocked.ID, loose.ID} {
			got, err := s.IsTaskBlocked(ctx, id)
			if err != nil {
				t.Fatalf("IsTaskBlocked(%s) %s: %v", id, when, err)
			}
			if got != ids[id] {
				t.Fatalf("IsTaskBlocked(%s) %s: got %v, BlockedTaskIDs says %v",
					id, when, got, ids[id])
			}
		}
		return ids
	}

	// With the blocker ready the edge bites — otherwise the agreement below
	// would hold vacuously, with nothing blocked either way.
	if ids := agree("with blocker ready"); !ids[blocked.ID] {
		t.Fatalf("fixture: %s should be blocked while %s is ready", blocked.ID, blocker.ID)
	}

	walkTo(t, s, blocker.ID, "merged")
	if ids := agree("with blocker merged"); ids[blocked.ID] {
		t.Fatalf("fixture: %s should be unblocked once %s merged", blocked.ID, blocker.ID)
	}
}

// TestBlockedTaskIDsDeliveredBlocker pins taskClosed for a blocker with no commit
// attribution: it gates on DefaultDoneState, so every state from merged onward
// leaves it unblocking. Narrowing that back to merged-or-abandoned would make
// these dependents block again.
func TestBlockedTaskIDsDeliveredBlocker(t *testing.T) {
	for _, state := range []string{"deployed_dev", "deployed_prod", "released"} {
		t.Run(state, func(t *testing.T) {
			s := openTaskStore(t)
			ctx := t.Context()

			blocker := createTask(t, s, taskTestNow, defaultTaskInput())
			blocked := createTask(t, s, taskTestNow, defaultTaskInput())
			if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
				t.Fatalf("AddEdge blocks: %v", err)
			}
			walkTo(t, s, blocker.ID, state)

			ids, err := s.BlockedTaskIDs(ctx)
			if err != nil {
				t.Fatalf("BlockedTaskIDs: %v", err)
			}
			if ids[blocked.ID] {
				t.Fatalf("BlockedTaskIDs with blocker %s: %s should be unblocked, got %v",
					state, blocked.ID, ids)
			}
			if isBlocked(t, s, blocked.ID) {
				t.Fatalf("IsBlocked(%s): want false with blocker %s", blocked.ID, state)
			}
		})
	}
}

func TestBlockedTaskIDsAbandonedBlocker(t *testing.T) {
	s := openTaskStore(t)

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := transition(t, s, taskTestNow, blocker.ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon blocker: %v", err)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: abandoned blocker must not block")
	}
}

// TestBlockedTaskIDsPerRepoDoneState pins the per-repo closed predicate
// (spec 004 §1.3): a blocker stops blocking at or past *its repo mapping's*
// done_state, not at one fixed tuple of states. The same merged blocker is
// closed in a repo that gates on merged and still open in one that gates on
// released.
func TestBlockedTaskIDsPerRepoDoneState(t *testing.T) {
	cases := []struct {
		doneState string
		state     string
		want      bool // want blocked
	}{
		{"merged", "merged", false},
		{"merged", "deployed_prod", false},
		{"deployed_prod", "merged", true},
		{"deployed_prod", "deployed_dev", true},
		{"deployed_prod", "deployed_prod", false},
		{"released", "merged", true},
		{"released", "deployed_dev", true},
		{"released", "released", false},
		// The two terminals are peers, not ordered (deliveryRanks): §5.1's
		// branches never meet, and there is no legal transition between them,
		// so treating either as short of the other would leave a task that
		// reached the wrong one blocking forever with nowhere to advance.
		{"released", "deployed_prod", false},
		{"deployed_prod", "released", false},
		// Abandoned is closed everywhere: cancelled work blocks nothing.
		{"released", "abandoned", false},
	}
	for _, tc := range cases {
		t.Run(tc.doneState+"/"+tc.state, func(t *testing.T) {
			s := openTaskStore(t)
			repo := "acme/" + tc.doneState
			mapRepo(t, s, "horndb", repo, tc.doneState)

			blocker := createTask(t, s, taskTestNow, defaultTaskInput())
			blocked := createTask(t, s, taskTestNow, defaultTaskInput())
			if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
				t.Fatalf("AddEdge blocks: %v", err)
			}
			landCommit(t, s, blocker.ID, repo, "sha-"+blocker.ID)
			walkTo(t, s, blocker.ID, tc.state)

			ids, err := s.BlockedTaskIDs(t.Context())
			if err != nil {
				t.Fatalf("BlockedTaskIDs: %v", err)
			}
			if ids[blocked.ID] != tc.want {
				t.Errorf("BlockedTaskIDs[%s] = %v, want %v (repo done_state %s, blocker %s)",
					blocked.ID, ids[blocked.ID], tc.want, tc.doneState, tc.state)
			}
			if got := isBlocked(t, s, blocked.ID); got != tc.want {
				t.Errorf("IsBlocked(%s) = %v, want %v (repo done_state %s, blocker %s)",
					blocked.ID, got, tc.want, tc.doneState, tc.state)
			}
		})
	}
}

// TestBlockedTaskIDsMultiRepoBlocker pins the multi-repo reading: a task whose
// work landed in two repos is closed only once it satisfies the strictest of
// them. Landing in a merged-gated repo does not release the release-gated one.
func TestBlockedTaskIDsMultiRepoBlocker(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/lib", "merged")
	mapRepo(t, s, "horndb", "acme/app", "released")

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, blocker.ID, "acme/lib", "sha-lib")
	landCommit(t, s, blocker.ID, "acme/app", "sha-app")

	walkTo(t, s, blocker.ID, "merged")
	if !isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: merged blocker with a release-gated repo must still block")
	}
	if err := transition(t, s, taskTestNow, blocker.ID, "merged", "released"); err != nil {
		t.Fatalf("release blocker: %v", err)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: released blocker must not block")
	}
}

// TestBlockedTaskIDsContainerBlocker pins the one state-fixed case (004 §6.4):
// a task with children has no commit of its own, cannot advance past merged,
// and is therefore closed at merged in every repo — including one whose
// mapping gates on released.
func TestBlockedTaskIDsContainerBlocker(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/app", "released")

	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, parent.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge child_of: %v", err)
	}
	if err := addEdge(t, s, parent.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, child.ID, "acme/app", "sha-child")

	// The child closing rolls the parent up to merged (004 §6.5).
	walkTo(t, s, child.ID, "released")
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: a container at merged must not block, whatever its repo gates on")
	}
}

// TestBlockedTaskIDsUnmappedRepoBlocker pins the fallback: a commit in a repo
// no project maps takes DefaultDoneState, so the blocker closes at merged
// rather than blocking forever on a done_state nobody configured.
func TestBlockedTaskIDsUnmappedRepoBlocker(t *testing.T) {
	s := openTaskStore(t)

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, blocker.ID, "acme/unmapped", "sha-unmapped")
	walkTo(t, s, blocker.ID, "merged")

	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: merged blocker in an unmapped repo must not block")
	}
}

// TestBlockedTaskIDsUnlandedCommitBlocker pins that taskClosed gates on the
// *landed* repo set, not on attribution: a task branch pushed to a
// release-gated repo writes a task_commits row even when that approach is
// abandoned and the work lands elsewhere. Gating on it would block the
// blocker's dependents forever, since ResolveDelivery walks the same
// task_commits ⋈ main_commits join and would never advance the task either.
func TestBlockedTaskIDsUnlandedCommitBlocker(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/app", "released")
	mapRepo(t, s, "horndb", "acme/lib", "merged")

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	pushBranchCommit(t, s, blocker.ID, "acme/app", "sha-abandoned")
	landCommit(t, s, blocker.ID, "acme/lib", "sha-landed")
	walkTo(t, s, blocker.ID, "merged")

	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: a branch push that never landed must not gate the blocker on that repo")
	}
}

// TestBlockedTaskIDsContainerWithOwnCommits pins §6.4's state-fixed case
// against the case the "a container has no commits" reading misses: AddEdge
// happily gives children to a task that already landed some. Such a parent is
// barred from every state past merged (containerForbiddenStates), so gating it
// on a release-based repo would block its dependents forever.
func TestBlockedTaskIDsContainerWithOwnCommits(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/app", "released")

	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, parent.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, parent.ID, "acme/app", "sha-parent")
	walkTo(t, s, parent.ID, "merged")

	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, parent.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge child_of: %v", err)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: a container at merged must not block, even carrying its own commits")
	}
}

// TestBlockedTaskIDsDoneStateFlipAfterDelivery pins that raising a repo's
// done_state after a task delivered cannot strand that task's dependents.
// Discovery runs only at add-repo (004 §5.4), so `lode project set-repo
// --done-state` on a repo that started cutting releases is the expected path,
// and a task already at deployed_prod has no legal transition left.
func TestBlockedTaskIDsDoneStateFlipAfterDelivery(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/app", "deployed_prod")

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, blocker.ID, "acme/app", "sha-app")
	walkTo(t, s, blocker.ID, "deployed_prod")
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: a blocker at its repo's done_state must not block")
	}

	if err := s.SetRepoDoneState(t.Context(), "acme/app", "released"); err != nil {
		t.Fatalf("SetRepoDoneState: %v", err)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: raising done_state must not strand an already-delivered blocker " +
			"(deployed_prod -> released is not a legal transition)")
	}
}

// TestOpenBlockersPerRepoDoneState covers the three other queries taskClosed
// is rendered into — each with a different set of enclosing aliases, so an
// alias collision or a mis-parenthesised predicate shows up here rather than
// only in IsBlocked. The blocker's repo belongs to a *second* project, which
// is what the deliberately project-unscoped repo join has to tolerate:
// 'blocks' edges are not project-scoped.
func TestOpenBlockersPerRepoDoneState(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "otherproj", "acme/app", "released")

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, blocker.ID, "acme/app", "sha-app")
	walkTo(t, s, blocker.ID, "merged")

	// brief.go: openBlockers.
	got, err := s.openBlockers(t.Context(), blocked.ID)
	if err != nil {
		t.Fatalf("openBlockers: %v", err)
	}
	if len(got) != 1 || got[0].ID != blocker.ID {
		t.Fatalf("openBlockers = %+v, want [%s] (merged, repo gates on released)", got, blocker.ID)
	}

	// project_work.go: attachOpenBlockers.
	facts := map[string]*ProjectWorkFact{blocked.ID: {}}
	if err := s.attachOpenBlockers(t.Context(), "horndb", facts); err != nil {
		t.Fatalf("attachOpenBlockers: %v", err)
	}
	if n := len(facts[blocked.ID].OpenBlockers); n != 1 {
		t.Fatalf("attachOpenBlockers left %d open blockers, want 1", n)
	}

	// ranking.go: readyCandidates, which renders blockedCondition into a
	// different query again.
	ready, err := s.readyCandidates(t.Context(), "horndb", "")
	if err != nil {
		t.Fatalf("readyCandidates: %v", err)
	}
	for _, cand := range ready {
		if cand.ID == blocked.ID {
			t.Fatalf("readyCandidates offered %s, which an undelivered blocker still blocks", blocked.ID)
		}
	}

	// Releasing the blocker clears it from all three.
	if err := transition(t, s, taskTestNow, blocker.ID, "merged", "released"); err != nil {
		t.Fatalf("release blocker: %v", err)
	}
	got, err = s.openBlockers(t.Context(), blocked.ID)
	if err != nil {
		t.Fatalf("openBlockers after release: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("openBlockers after release = %+v, want none", got)
	}
	ready, err = s.readyCandidates(t.Context(), "horndb", "")
	if err != nil {
		t.Fatalf("readyCandidates after release: %v", err)
	}
	if !slices.ContainsFunc(ready, func(c model.Task) bool { return c.ID == blocked.ID }) {
		t.Fatalf("readyCandidates after release omitted %s", blocked.ID)
	}
}
