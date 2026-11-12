package api

import (
	"context"
	"errors"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// eventRetentionEvery is how often the event retention sweep runs
// (WL-REQ-1895). It reuses lockSweepLoop.
const eventRetentionEvery = time.Hour

// sweepEvents deletes old unreferenced events by rules. A failure is logged
// and counted, and the next tick retries.
func (s *server) sweepEvents(ctx context.Context, rules []store.RetentionRule, m *retentionMetrics) {
	deleted, err := s.st.SweepEvents(ctx, rules, time.Now())
	m.deleted(deleted)
	if err != nil && !errors.Is(err, context.Canceled) {
		m.failed()
		s.log.Error("event retention sweep failed", "err", err)
	}
}
