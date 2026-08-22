package store

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestSweepLeasesCountsRuns asserts the loop reclaims an expired lease and
// counts the tick under result="ok".
func TestSweepLeasesCountsRuns(t *testing.T) {
	s, now := openLeaseStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.Claim(t.Context(), task.ID, "stig", "host:/wt-a", time.Second); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// The loop reads the clock itself, so move it past the lease TTL first.
	*now = now.Add(time.Hour)

	// Run the loop until it has swept at least once, then cancel it.
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.sweepLeases(ctx, time.Millisecond)
	}()
	waitFor(t, func() bool {
		return testutil.ToFloat64(s.metrics.expiries) == 1
	}, "sweeper never expired the lease")
	cancel()
	<-done

	if got := testutil.ToFloat64(s.metrics.sweeperRuns.WithLabelValues("ok")); got < 1 {
		t.Fatalf("sweeper_runs{ok} = %v, want >= 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.sweeperRuns.WithLabelValues("error")); got != 0 {
		t.Fatalf("sweeper_runs{error} = %v, want 0", got)
	}
	if active, _ := countLeases(t, s, task.ID); active != 0 {
		t.Fatalf("active leases after sweep = %d, want 0", active)
	}
}

// TestSweepLeasesCountsErrors asserts a failing sweep counts under
// result="error" and does not end the loop. The store's pool is closed, so
// every ExpireLeases call fails.
func TestSweepLeasesCountsErrors(t *testing.T) {
	s, _ := openLeaseStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	s.db.Close()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.sweepLeases(ctx, time.Millisecond)
	}()
	waitFor(t, func() bool {
		return testutil.ToFloat64(s.metrics.sweeperRuns.WithLabelValues("error")) >= 2
	}, "sweeper did not keep counting errors")
	cancel()
	<-done

	if got := testutil.ToFloat64(s.metrics.sweeperRuns.WithLabelValues("ok")); got != 0 {
		t.Fatalf("sweeper_runs{ok} = %v, want 0", got)
	}
}

// TestSweepLeasesIgnoresASweepTornDownByShutdown asserts a sweep that fails
// with the context already done counts as neither ok nor error. Shutdown
// aborts the in-flight round-trip, so the failure arrives as the pool's own
// error ("sql: database is closed" here, "write tcp ...: i/o timeout" against
// a live pool) and never as a wrapped context.Canceled — matching on the
// error's shape would count shutdown as a failed sweep.
func TestSweepLeasesIgnoresASweepTornDownByShutdown(t *testing.T) {
	s, _ := openLeaseStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	s.db.Close() // every ExpireLeases call now fails

	ctx, cancel := context.WithCancel(t.Context())
	// The loop reads the clock after the tick and before the sweep, so
	// cancelling from nowFn puts the context in exactly the state shutdown
	// leaves it in: done, with a sweep already under way.
	s.SetNowFunc(func() time.Time {
		cancel()
		return leaseTestNow
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.sweepLeases(ctx, time.Millisecond)
	}()
	<-done

	for _, result := range []string{"ok", "error"} {
		if got := testutil.ToFloat64(s.metrics.sweeperRuns.WithLabelValues(result)); got != 0 {
			t.Fatalf("sweeper_runs{%s} = %v after a sweep torn down by shutdown, want 0", result, got)
		}
	}
}

// TestNewStoreMetricsPreInitialisesSweeperSeries asserts both result series
// exist at zero before the sweeper has ticked, so an alert expression sees 0
// rather than no-data.
func TestNewStoreMetricsPreInitialisesSweeperSeries(t *testing.T) {
	m := newStoreMetrics(prometheus.NewRegistry())
	for _, result := range []string{"ok", "error"} {
		if got := testutil.ToFloat64(m.sweeperRuns.WithLabelValues(result)); got != 0 {
			t.Fatalf("sweeper_runs{%s} = %v, want 0", result, got)
		}
	}
	if got := testutil.CollectAndCount(m.sweeperRuns); got != 2 {
		t.Fatalf("sweeper_runs series = %d, want 2", got)
	}
}

// waitFor polls cond until it holds or the test times out.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(msg)
}
