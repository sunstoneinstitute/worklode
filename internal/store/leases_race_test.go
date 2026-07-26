package store

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestClaimRace fires n concurrent Claims at one ready task: exactly one
// wins; every loser gets ErrLeased; the task ends in_progress with exactly
// one active lease. (spec 004 acceptance criterion 4)
func TestClaimRace(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	const n = 16
	var wins, losses atomic.Int32
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Claim(ctx, task.ID, "stig", fmt.Sprintf("host:/wt-%d", i), DefaultLeaseTTL)
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrLeased):
				losses.Add(1)
			default:
				t.Errorf("goroutine %d: unexpected claim error: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if wins.Load() != 1 || losses.Load() != n-1 {
		t.Fatalf("claims: wins=%d losses=%d, want 1 and %d", wins.Load(), losses.Load(), n-1)
	}

	mustState(t, s, task.ID, "in_progress")
	active, total := countLeases(t, s, task.ID)
	if active != 1 || total != 1 {
		t.Fatalf("lease rows: active=%d total=%d, want 1 and 1", active, total)
	}
}
