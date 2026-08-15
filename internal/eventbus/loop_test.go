package eventbus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Loop tests run on 10–20 ms intervals so a whole test is well under a
// second in the good case, and poll to a bounded deadline for anything that
// depends on the commit horizon: pg_snapshot_xmin is cluster-wide, not
// per-test-database, so a concurrent transaction anywhere on this Postgres
// instance can hold a read back to zero events after the events under test
// have committed. Polling absorbs that without weakening the assertions —
// a loop that delivers out of order, skips, or acks too far still fails.
const (
	testPoll      = 10 * time.Millisecond
	testLockRetry = 20 * time.Millisecond
	waitDeadline  = 15 * time.Second
)

// recorder collects the ids a handler was called with.
type recorder struct {
	mu  sync.Mutex
	ids []int64
}

func (r *recorder) handle(ctx context.Context, ev store.Event) (Outcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, ev.ID)
	return OutcomeApplied, nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ids)
}

func (r *recorder) snapshot() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ids)
}

// recordTestEvents appends n events with a dotted (non-vocabulary) type, so
// the metrics tests exercise the "other" type label (025 §15.7).
func recordTestEvents(t *testing.T, ctx context.Context, s *store.Store, prefix string, n int) []int64 {
	t.Helper()
	ids := make([]int64, 0, n)
	for i := range n {
		id, inserted, err := s.RecordEvent(ctx, "system",
			fmt.Sprintf("%s-%d", prefix, i), "test.event", []byte(`{}`), nil)
		if err != nil {
			t.Fatalf("RecordEvent %s-%d: %v", prefix, i, err)
		}
		if !inserted {
			t.Fatalf("RecordEvent %s-%d: not inserted", prefix, i)
		}
		ids = append(ids, id)
	}
	return ids
}

// waitFor polls cond to a bounded deadline and fails on timeout — never
// treating "not yet" as success.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s "+
				"(commit horizon held back by a concurrent transaction elsewhere on the instance?)", what)
		}
		time.Sleep(testPoll)
	}
}

// readAckedOffset returns one subscriber's last_acked_offset, or -1 if
// absent. It takes no *testing.T so a handler — which runs on the loop
// goroutine, where t.Fatalf would Goexit the wrong goroutine — can call it
// and report back to the test goroutine.
func readAckedOffset(s *store.Store, name string) (int64, error) {
	subs, err := s.EventSubscribers(context.Background())
	if err != nil {
		return 0, err
	}
	for _, sub := range subs {
		if sub.Name == name {
			return sub.LastAcked, nil
		}
	}
	return -1, nil
}

// ackedOffset is readAckedOffset for the test goroutine only.
func ackedOffset(t *testing.T, s *store.Store, name string) int64 {
	t.Helper()
	off, err := readAckedOffset(s, name)
	if err != nil {
		t.Fatalf("EventSubscribers: %v", err)
	}
	return off
}

// startLoop runs one loop in a goroutine and returns a stop func that
// cancels it and waits for Run to return the expected context.Canceled.
func startLoop(t *testing.T, ctx context.Context, o Options) (stop func()) {
	t.Helper()
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- Run(loopCtx, o) }()
	stop = func() {
		t.Helper()
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Run returned %v, want context.Canceled", err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("Run did not return within 10s of cancel")
		}
	}
	t.Cleanup(func() { cancel() })
	return stop
}

func loopOptions(s *store.Store, name string, h Handler) Options {
	return Options{
		Store:     s,
		Name:      name,
		Handler:   h,
		Poll:      testPoll,
		LockRetry: testLockRetry,
		BatchSize: 10,
	}
}

// TestLoopDeliversInOrderAndAcks: the loop hands every event to the handler
// in id order and acks the batch (025 §15.1).
func TestLoopDeliversInOrderAndAcks(t *testing.T) {
	s := store.OpenTestStore(t)
	ctx := t.Context()
	ids := recordTestEvents(t, ctx, s, "a", 3)
	if err := s.EnsureEventSubscriber(ctx, "docs"); err != nil {
		t.Fatalf("EnsureEventSubscriber: %v", err)
	}

	var rec recorder
	stop := startLoop(t, ctx, loopOptions(s, "docs", rec.handle))

	last := ids[len(ids)-1]
	waitFor(t, "3 events handled and acked", func() bool {
		return rec.count() == 3 && ackedOffset(t, s, "docs") == last
	})
	stop()

	if got := rec.snapshot(); !slices.Equal(got, ids) {
		t.Fatalf("handled ids = %v, want %v (in order)", got, ids)
	}
	if got := ackedOffset(t, s, "docs"); got != last {
		t.Fatalf("last_acked_offset = %d, want %d", got, last)
	}
}

// TestLoopSingleConsumer: one subscriber has exactly one active consumer
// (025 §15.1). The standby idles on the lock and takes over when the holder
// exits, resuming at last_acked_offset — it must not replay acked events.
func TestLoopSingleConsumer(t *testing.T) {
	s := store.OpenTestStore(t)
	ctx := t.Context()
	first := recordTestEvents(t, ctx, s, "a", 3)
	if err := s.EnsureEventSubscriber(ctx, "docs"); err != nil {
		t.Fatalf("EnsureEventSubscriber: %v", err)
	}

	recs := []*recorder{{}, {}}
	stops := make([]func(), 2)
	for i := range recs {
		stops[i] = startLoop(t, ctx, loopOptions(s, "docs", recs[i].handle))
	}

	lastFirst := first[len(first)-1]
	waitFor(t, "one loop to consume and ack the first 3 events", func() bool {
		return recs[0].count()+recs[1].count() == 3 && ackedOffset(t, s, "docs") == lastFirst
	})

	consumer := 0
	if recs[1].count() > 0 {
		consumer = 1
	}
	standby := 1 - consumer
	if recs[standby].count() != 0 {
		t.Fatalf("both loops consumed: %v and %v", recs[0].snapshot(), recs[1].snapshot())
	}
	if got := recs[consumer].snapshot(); !slices.Equal(got, first) {
		t.Fatalf("consumer handled %v, want %v", got, first)
	}

	// The holder exits; the standby must acquire the lock on its next retry.
	stops[consumer]()

	second := recordTestEvents(t, ctx, s, "b", 2)
	waitFor(t, "the standby to take over the stream", func() bool {
		return recs[standby].count() == 2
	})
	if got := recs[standby].snapshot(); !slices.Equal(got, second) {
		t.Fatalf("standby handled %v, want %v (acked events must not replay)", got, second)
	}
	stops[standby]()
}

// lockHolderPID returns the backend pid holding the wl:subscriber advisory
// lock for name, or 0 if nobody holds it. Same join as the store's own
// advisoryLockHolderPID (and Task 7's status view): classid is the high 32
// bits of the key, objid the low 32, objsubid 1 for the single-bigint form.
func lockHolderPID(t *testing.T, s *store.Store, name string) int {
	t.Helper()
	var pid int
	err := s.DBForTests().QueryRowContext(context.Background(), `
		SELECT pid FROM pg_locks
		 WHERE locktype = 'advisory'
		   AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
		   AND classid = ((hashtext('wl:subscriber:' || $1)::bigint >> 32) & 4294967295)::oid
		   AND objid = (hashtext('wl:subscriber:' || $1)::bigint & 4294967295)::oid
		   AND objsubid = 1
		   AND granted`, name).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("query advisory lock holder for %s: %v", name, err)
	}
	return pid
}

// TestLoopReacquiresAfterLockSessionLost: the lock connection is idle
// between polls, so an idle_session_timeout, a pooler reap or a
// pg_terminate_backend can take it without any pooled call erroring. The
// loop must notice (the per-iteration health check) and re-acquire —
// otherwise it keeps consuming a stream a standby is free to pick up, and
// forward-only acks would swallow the other consumer's head-of-line retry.
func TestLoopReacquiresAfterLockSessionLost(t *testing.T) {
	s := store.OpenTestStore(t)
	ctx := t.Context()
	first := recordTestEvents(t, ctx, s, "a", 2)
	if err := s.EnsureEventSubscriber(ctx, "docs"); err != nil {
		t.Fatalf("EnsureEventSubscriber: %v", err)
	}

	var rec recorder
	stop := startLoop(t, ctx, loopOptions(s, "docs", rec.handle))

	waitFor(t, "the loop to consume and ack the first events", func() bool {
		return rec.count() == 2 && ackedOffset(t, s, "docs") == first[len(first)-1]
	})
	held := lockHolderPID(t, s, "docs")
	if held == 0 {
		t.Fatalf("no backend holds the subscriber lock while the loop is consuming")
	}

	// Kill the lock session out from under the loop. The pool is untouched:
	// nothing the loop calls will fail because of this.
	if _, err := s.DBForTests().ExecContext(ctx,
		`SELECT pg_terminate_backend($1)`, held); err != nil {
		t.Fatalf("terminate lock backend %d: %v", held, err)
	}

	waitFor(t, "the loop to re-acquire the subscriber lock on a new session", func() bool {
		pid := lockHolderPID(t, s, "docs")
		return pid != 0 && pid != held
	})

	// And it is still a working consumer afterwards.
	second := recordTestEvents(t, ctx, s, "b", 2)
	waitFor(t, "the re-acquired loop to consume the new events", func() bool {
		return rec.count() == 4
	})
	stop()

	want := append(slices.Clone(first), second...)
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("handled ids = %v, want %v (in order, no replay)", got, want)
	}
}

// TestLoopRedeliversAfterHandlerError: a failing handler stops the batch.
// The prefix already handled is acked, the failed event is redelivered on
// the next poll and blocks everything behind it until it succeeds
// (at-least-once, in order, no DLQ).
func TestLoopRedeliversAfterHandlerError(t *testing.T) {
	s := store.OpenTestStore(t)
	ctx := t.Context()
	ids := recordTestEvents(t, ctx, s, "a", 3)
	if err := s.EnsureEventSubscriber(ctx, "docs"); err != nil {
		t.Fatalf("EnsureEventSubscriber: %v", err)
	}

	var mu sync.Mutex
	var calls []int64
	failures := 0
	ackedAtSuccess := int64(-1)
	var ackedAtSuccessErr error

	// The handler runs on the loop goroutine, so it reports failures back to
	// the test goroutine instead of calling t.Fatalf — a Fatalf here would
	// Goexit the loop mid-batch and surface as a mystery timeout.
	handler := func(hctx context.Context, ev store.Event) (Outcome, error) {
		mu.Lock()
		calls = append(calls, ev.ID)
		fail := ev.ID == ids[1] && failures < 2
		if fail {
			failures++
		}
		mu.Unlock()
		if fail {
			return "", fmt.Errorf("handler boom on %d", ev.ID)
		}
		if ev.ID == ids[1] {
			// Prefix-ack: id[0] is durable before id[1] ever succeeds.
			off, err := readAckedOffset(s, "docs")
			mu.Lock()
			ackedAtSuccess, ackedAtSuccessErr = off, err
			mu.Unlock()
		}
		return OutcomeApplied, nil
	}

	stop := startLoop(t, ctx, loopOptions(s, "docs", handler))

	last := ids[len(ids)-1]
	waitFor(t, "every event handled and acked", func() bool {
		return ackedOffset(t, s, "docs") == last
	})
	stop()

	mu.Lock()
	got := slices.Clone(calls)
	acked := ackedAtSuccess
	ackedErr := ackedAtSuccessErr
	mu.Unlock()
	if ackedErr != nil {
		t.Fatalf("reading the acked offset from the handler: %v", ackedErr)
	}

	want := []int64{ids[0], ids[1], ids[1], ids[1], ids[2]}
	if !slices.Equal(got, want) {
		t.Fatalf("handler calls = %v, want %v (failed event redelivered, nothing behind it)", got, want)
	}
	if acked < ids[0] {
		t.Fatalf("last_acked_offset when id %d finally succeeded = %d, want >= %d (prefix-ack)",
			ids[1], acked, ids[0])
	}
}

// TestLoopMetrics asserts the 025 §15.7 instruments: the processed counter
// with a bounded type label, the batch-duration histogram, and the lag gauge
// (up with events pending, back to zero once the loop catches up).
func TestLoopMetrics(t *testing.T) {
	s := store.OpenTestStore(t)
	ctx := t.Context()
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg, s)

	recordTestEvents(t, ctx, s, "a", 3)
	if err := s.EnsureEventSubscriber(ctx, "docs"); err != nil {
		t.Fatalf("EnsureEventSubscriber: %v", err)
	}

	waitFor(t, "lag > 0 with events pending and no consumer", func() bool {
		return gaugeValue(t, reg, "worklode_event_subscriber_lag", "subscriber", "docs") > 0
	})

	var rec recorder
	o := loopOptions(s, "docs", rec.handle)
	o.Metrics = m
	stop := startLoop(t, ctx, o)

	waitFor(t, "the loop to catch up (lag back to 0)", func() bool {
		return rec.count() == 3 && gaugeValue(t, reg, "worklode_event_subscriber_lag", "subscriber", "docs") == 0
	})
	stop()

	// type="other": the test events carry a dotted, non-vocabulary type.
	if got := testutil.ToFloat64(m.processed.WithLabelValues("docs", "other", "applied")); got != 3 {
		t.Fatalf("worklode_events_processed_total{docs,other,applied} = %v, want 3", got)
	}
	if n := histogramCount(t, reg, "worklode_event_batch_duration_seconds", "subscriber", "docs"); n == 0 {
		t.Fatalf("worklode_event_batch_duration_seconds{docs} observed %d times, want > 0", n)
	}
}

// TestMetricsNilSafe: a loop configured without metrics records nothing and
// does not panic (022 convention).
func TestMetricsNilSafe(t *testing.T) {
	var m *Metrics
	m.event("docs", "test.event", "applied")
	m.batch("docs", 0.01)
}

// TestMetricsBoundsTypeLabel: a known vocabulary type is kept, anything else
// collapses to "other" (025 §15.7).
func TestMetricsBoundsTypeLabel(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg, nil)
	m.event("docs", TypeDocumentAccepted, "applied")
	m.event("docs", "github.push", "applied")
	m.event("docs", "wl:NotAThing", "error")

	if got := testutil.ToFloat64(m.processed.WithLabelValues("docs", TypeDocumentAccepted, "applied")); got != 1 {
		t.Fatalf("processed{%s,applied} = %v, want 1", TypeDocumentAccepted, got)
	}
	if got := testutil.ToFloat64(m.processed.WithLabelValues("docs", "other", "applied")); got != 1 {
		t.Fatalf("processed{other,applied} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.processed.WithLabelValues("docs", "other", "error")); got != 1 {
		t.Fatalf("processed{other,error} = %v, want 1", got)
	}
}

// TestLagCollectorSurfacesQueryFailure: a lag query that fails must surface
// as a scrape error, never as a stale zero that an alert would read as
// "caught up" (the worklode_leases_active convention).
func TestLagCollectorSurfacesQueryFailure(t *testing.T) {
	s := store.OpenTestStore(t)
	reg := prometheus.NewRegistry()
	NewMetrics(reg, s)

	if _, err := reg.Gather(); err != nil {
		t.Fatalf("gather against a healthy store: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := reg.Gather(); err == nil {
		t.Fatalf("gather after the store closed: want a scrape error, got none")
	}
}

// gaugeValue reads one labelled sample of a gauge family from reg. Returns
// -1 when the family or the sample is absent.
func gaugeValue(t *testing.T, g prometheus.Gatherer, family, label, value string) float64 {
	t.Helper()
	m := findMetric(t, g, family, label, value)
	if m == nil {
		return -1
	}
	return m.GetGauge().GetValue()
}

// histogramCount reads one labelled histogram's sample count from reg.
func histogramCount(t *testing.T, g prometheus.Gatherer, family, label, value string) uint64 {
	t.Helper()
	m := findMetric(t, g, family, label, value)
	if m == nil {
		return 0
	}
	return m.GetHistogram().GetSampleCount()
}

func findMetric(t *testing.T, g prometheus.Gatherer, family, label, value string) *dto.Metric {
	t.Helper()
	mfs, err := g.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != family {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == label && lp.GetValue() == value {
					return m
				}
			}
		}
	}
	return nil
}
