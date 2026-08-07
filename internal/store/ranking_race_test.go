package store

import (
	"fmt"
	"sync"
	"testing"
)

// TestClaimNextNoCollisionUnderContention pins spec-02 acceptance criterion
// 1: with M ready tasks in one project, firing N > M concurrent ClaimNext
// calls (each from a distinct worktree, same actor) yields exactly M
// distinct winners, the rest report Claimed:false, and no call errors.
func TestClaimNextNoCollisionUnderContention(t *testing.T) {
	s := openClaimNextStore(t)
	ctx := t.Context()

	const m = 4
	tasks := make([]*Task, m)
	for i := range m {
		tasks[i] = createTask(t, s, claimNextTestNow, defaultTaskInput())
	}

	const n = 8
	results := make([]*ClaimNextResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.ClaimNext(ctx, ClaimNextOpts{
				ActorID:  "stig",
				Worktree: fmt.Sprintf("h:/.worktrees/%d", i),
			})
		}(i)
	}
	wg.Wait()

	var claimed, unclaimed int
	claimedIDs := map[string]bool{}
	for i := range n {
		if errs[i] != nil {
			t.Errorf("goroutine %d: unexpected ClaimNext error: %v", i, errs[i])
			continue
		}
		res := results[i]
		if res == nil {
			t.Errorf("goroutine %d: nil result with no error", i)
			continue
		}
		if res.Claimed {
			claimed++
			if res.Task == nil {
				t.Errorf("goroutine %d: Claimed:true but Task is nil", i)
				continue
			}
			if claimedIDs[res.Task.ID] {
				t.Errorf("goroutine %d: task %s claimed more than once", i, res.Task.ID)
			}
			claimedIDs[res.Task.ID] = true
		} else {
			unclaimed++
		}
	}

	if claimed != m {
		t.Fatalf("claimed=%d, want %d", claimed, m)
	}
	if unclaimed != n-m {
		t.Fatalf("unclaimed=%d, want %d", unclaimed, n-m)
	}
	if len(claimedIDs) != m {
		t.Fatalf("distinct claimed task ids=%d, want %d (got %v)", len(claimedIDs), m, claimedIDs)
	}
	for _, task := range tasks {
		if !claimedIDs[task.ID] {
			t.Errorf("task %s was never claimed", task.ID)
		}
	}
}
