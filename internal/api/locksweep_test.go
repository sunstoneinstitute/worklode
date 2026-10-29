package api

import (
	"context"
	"testing"
	"time"
)

// A cancelled context gets one sweep, then the loop returns.
func TestLockSweepLoopRunsOnceAndStops(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	done := make(chan struct{})
	go func() {
		lockSweepLoop(ctx, time.Hour, func(context.Context) { calls++ })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop on a cancelled context")
	}
	if calls != 1 {
		t.Fatalf("sweeps = %d, want 1", calls)
	}
}

func TestLockSweepLoopTicks(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ran := make(chan struct{}, 8)
	go lockSweepLoop(ctx, 5*time.Millisecond, func(context.Context) { ran <- struct{}{} })
	for range 3 {
		select {
		case <-ran:
		case <-time.After(5 * time.Second):
			t.Fatal("loop stopped ticking")
		}
	}
}
