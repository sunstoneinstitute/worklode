package api

import (
	"context"
	"errors"
	"time"
)

// lockSweepEvery is how often the body lock sweep runs (WL-REQ-1868).
const lockSweepEvery = time.Minute

// lockSweepLoop calls sweep once on start, then every interval until ctx is
// done. NewServer starts it only when it gets a BackgroundCtx.
func lockSweepLoop(ctx context.Context, every time.Duration, sweep func(context.Context)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sweepLocks ends idle and expired body lock sessions. A failure is logged
// and the next tick retries.
func (s *server) sweepLocks(ctx context.Context) {
	if _, _, err := s.st.SweepLocks(ctx, time.Now()); err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("body lock sweep failed", "err", err)
	}
}
