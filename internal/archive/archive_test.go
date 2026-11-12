package archive

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// fakeExporter records exported records and fails while fail is set.
type fakeExporter struct {
	mu       sync.Mutex
	fail     bool
	attempts []int64 // event id of every Export call, failed ones included
	got      []sdklog.Record
}

func (f *fakeExporter) Export(_ context.Context, recs []sdklog.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range recs {
		f.attempts = append(f.attempts, eventID(r))
	}
	if f.fail {
		return errors.New("collector down")
	}
	for _, r := range recs {
		f.got = append(f.got, r.Clone())
	}
	return nil
}
func (f *fakeExporter) Shutdown(context.Context) error   { return nil }
func (f *fakeExporter) ForceFlush(context.Context) error { return nil }

func (f *fakeExporter) setFail(v bool) { f.mu.Lock(); f.fail = v; f.mu.Unlock() }

func (f *fakeExporter) snapshot() (attempts []int64, got []sdklog.Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.attempts), slices.Clone(f.got)
}

func attrs(r sdklog.Record) map[string]log.Value {
	m := map[string]log.Value{}
	r.WalkAttributes(func(kv log.KeyValue) bool { m[kv.Key] = kv.Value; return true })
	return m
}

func eventID(r sdklog.Record) int64 { return attrs(r)["worklode.event.id"].AsInt64() }

func recordEvents(t *testing.T, s *store.Store, n int) []int64 {
	t.Helper()
	var ids []int64
	for i := range n {
		id, _, err := s.RecordEvent(t.Context(), "github", fmt.Sprintf("delivery-%d", i),
			"push", fmt.Appendf(nil, `{"n":%d}`, i), nil)
		if err != nil {
			t.Fatalf("RecordEvent: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func acked(t *testing.T, s *store.Store) int64 {
	t.Helper()
	subs, err := s.EventSubscribers(context.Background())
	if err != nil {
		t.Fatalf("EventSubscribers: %v", err)
	}
	for _, sub := range subs {
		if sub.Name == Subscriber {
			return sub.LastAcked
		}
	}
	t.Fatalf("no %s subscriber row", Subscriber)
	return 0
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// run starts the archive subscriber on s and returns its error counter.
func run(t *testing.T, s *store.Store, exp sdklog.Exporter) prometheus.Counter {
	t.Helper()
	if err := s.EnsureEventSubscriber(t.Context(), Subscriber); err != nil {
		t.Fatalf("EnsureEventSubscriber: %v", err)
	}
	m := NewMetrics(prometheus.NewRegistry())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eventbus.Run(ctx, eventbus.Options{
			Store: s, Name: Subscriber, Handler: New(exp, m).Handle,
			Poll: 10 * time.Millisecond, LockRetry: 20 * time.Millisecond,
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return m.errors
}

func TestArchiveExportsInOrderWithAttributes(t *testing.T) {
	s := store.OpenTestStore(t)
	ids := recordEvents(t, s, 3)
	exp := &fakeExporter{}
	run(t, s, exp)

	last := ids[len(ids)-1]
	waitFor(t, "3 events exported and acked", func() bool { return acked(t, s) == last })

	_, got := exp.snapshot()
	var gotIDs []int64
	for _, r := range got {
		gotIDs = append(gotIDs, eventID(r))
	}
	if !slices.Equal(gotIDs, ids) {
		t.Fatalf("exported ids = %v, want %v", gotIDs, ids)
	}
	r := got[1]
	if r.EventName() != EventName {
		t.Errorf("event name = %q, want %q", r.EventName(), EventName)
	}
	if b := r.Body().AsString(); b != `{"n": 1}` && b != `{"n":1}` {
		t.Errorf("body = %q, want the event payload", b)
	}
	a := attrs(r)
	for k, want := range map[string]string{
		"worklode.event.source":      "github",
		"worklode.event.type":        "push",
		"worklode.event.external_id": "delivery-1",
	} {
		if v := a[k].AsString(); v != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, a["worklode.event.received_at"].AsString()); err != nil {
		t.Errorf("worklode.event.received_at: %v", err)
	}
}

func TestArchiveFailedExportRetriesFromSameOffset(t *testing.T) {
	s := store.OpenTestStore(t)
	ids := recordEvents(t, s, 2)
	exp := &fakeExporter{fail: true}
	errs := run(t, s, exp)
	start := acked(t, s)

	waitFor(t, "two failed export attempts", func() bool {
		a, _ := exp.snapshot()
		return len(a) >= 2
	})
	if got := acked(t, s); got != start {
		t.Fatalf("acked offset moved to %d after failed exports, want %d", got, start)
	}
	attempts, _ := exp.snapshot()
	for _, id := range attempts {
		if id != ids[0] {
			t.Fatalf("attempts = %v, want only the head event %d retried", attempts, ids[0])
		}
	}
	if v := testutil.ToFloat64(errs); v < 2 {
		t.Fatalf("worklode_events_archive_errors_total = %v, want >= 2", v)
	}

	exp.setFail(false)
	waitFor(t, "resume and ack", func() bool { return acked(t, s) == ids[1] })
	_, got := exp.snapshot()
	if len(got) != 2 || eventID(got[0]) != ids[0] || eventID(got[1]) != ids[1] {
		t.Fatalf("exported after resume: %d records, want ids %v", len(got), ids)
	}
}

func TestEnabled(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want bool
	}{
		{nil, false},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://gw:4318"}, true},
		{map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://gw:4318/v1/logs"}, true},
		{map[string]string{"OTEL_EXPORTER_OTLP_HEADERS": "a=b"}, false},
	} {
		if got := Enabled(func(k string) string { return tc.env[k] }); got != tc.want {
			t.Errorf("Enabled(%v) = %v, want %v", tc.env, got, tc.want)
		}
	}
}
