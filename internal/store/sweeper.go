package store

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// leaseSweepInterval is how often a serving store expires stale leases. Well
// under the shortest lease TTL, so a reclaimed task is back in ready within
// about a minute of its holder going quiet.
const leaseSweepInterval = 60 * time.Second

// StartLeaseSweeper runs ExpireLeases on a background goroutine every
// leaseSweepInterval until ctx is cancelled. The loop and its
// worklode_lease_sweeper_runs_total counter live here rather than in the
// serve command because the sweep is this package's operation (022 §4).
// Callers get the counter by opening the store WithMetrics.
func (s *Store) StartLeaseSweeper(ctx context.Context) {
	go s.sweepLeases(ctx, leaseSweepInterval)
}

// sweepLeases is StartLeaseSweeper's loop, with the interval as a parameter so
// tests need not wait a minute for a tick. A sweep that fails because ctx was
// cancelled (shutdown) ends the loop and records nothing — neither ok nor
// error — so shutdown does not spike the error rate; every other outcome is
// counted and the loop continues.
func (s *Store) sweepLeases(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.ExpireLeases(ctx, s.nowFn().UTC())
			if errors.Is(err, context.Canceled) {
				return
			}
			s.metrics.sweeperRun(err)
			if err != nil {
				slog.Error("expire leases", "err", err)
				continue
			}
			if n > 0 {
				slog.Info("expired leases", "count", n)
			}
		}
	}
}
