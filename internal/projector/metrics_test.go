package projector_test

import (
	"database/sql"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/projector"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// newMetricsProjector is newProjector's counterpart for tests that assert on
// counter movement: it wires a real *projector.Metrics on its own registry
// instead of the nil metrics newProjector passes.
func newMetricsProjector(t *testing.T) (*store.Store, *projector.Projector, *fakeGraphServer, *projector.Metrics) {
	t.Helper()
	s := store.OpenTestStore(t)
	if err := s.CreateProject(t.Context(), "alpha", "Alpha", "AL"); err != nil {
		t.Fatalf("create project alpha: %v", err)
	}
	f := &fakeGraphServer{}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	reg := prometheus.NewRegistry()
	m := projector.NewMetrics(reg)
	return s, projector.New(s, graphserver.New(srv.URL, nil), m, 100), f, m
}

func TestMetricsSuccessfulRunIncrementsOkAndProjects(t *testing.T) {
	s, p, _, m := newMetricsProjector(t)
	createTask(t, s, "m1", "alpha", "wire the metrics")

	n, err := p.RunOnce(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v; want 1, nil", n, err)
	}

	if got := testutil.ToFloat64(m.Runs().WithLabelValues("ok")); got != 1 {
		t.Errorf("runs_total{ok} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.Runs().WithLabelValues("error")); got != 0 {
		t.Errorf("runs_total{error} = %v, want 0 (pre-initialised, not no-data)", got)
	}
	if got := testutil.ToFloat64(m.Projects()); got != 1 {
		t.Errorf("projects_total = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(m.Duration()); got != 1 {
		t.Errorf("duration_seconds observation count = %d, want 1", got)
	}
}

func TestMetricsFailingRunIncrementsErrorLeavesProjects(t *testing.T) {
	s, p, f, m := newMetricsProjector(t)
	createTask(t, s, "m2", "alpha", "unlucky")

	f.setFail(true)
	if _, err := p.RunOnce(t.Context()); err == nil {
		t.Fatal("RunOnce against a failing endpoint returned nil error")
	}

	if got := testutil.ToFloat64(m.Runs().WithLabelValues("error")); got != 1 {
		t.Errorf("runs_total{error} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.Runs().WithLabelValues("ok")); got != 0 {
		t.Errorf("runs_total{ok} = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.Projects()); got != 0 {
		t.Errorf("projects_total = %v, want 0 (nothing was successfully PUT)", got)
	}
}

// TestNilMetricsRecordsNothing exercises both the success and error paths
// through RunOnce with a nil *Metrics (as newProjector's tests already do
// elsewhere) — the point here is documenting that recording is nil-safe,
// not introducing new coverage of RunOnce's control flow.
func TestNilMetricsRecordsNothing(t *testing.T) {
	s, p, f := newProjector(t)
	createTask(t, s, "m3", "alpha", "no metrics wired")

	if _, err := p.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce with nil metrics: %v", err)
	}

	f.setFail(true)
	createTask(t, s, "m4", "alpha", "still no metrics")
	if _, err := p.RunOnce(t.Context()); err == nil {
		t.Fatal("RunOnce against a failing endpoint returned nil error")
	}
}

// TestIdlePollSkipsCheckpointWrite proves ruling (b): DirtyProjects returning
// an empty batch (through == cp) must not issue a checkpoint UPDATE. xmin is
// a Postgres system column that changes on every tuple rewrite, including a
// no-op UPDATE that sets the same value, so an unchanged xmin is direct
// evidence no write happened.
func TestIdlePollSkipsCheckpointWrite(t *testing.T) {
	s, p, _ := newProjector(t)
	before := graphProjectionXmin(t, s)

	n, err := p.RunOnce(t.Context())
	if err != nil || n != 0 {
		t.Fatalf("idle RunOnce = %d, %v; want 0, nil", n, err)
	}

	after := graphProjectionXmin(t, s)
	if before != after {
		t.Errorf("idle RunOnce rewrote graph_projection: xmin %s -> %s", before, after)
	}
}

func graphProjectionXmin(t *testing.T, s *store.Store) string {
	t.Helper()
	var x string
	err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT xmin::text FROM graph_projection WHERE id = 1`).Scan(&x)
	})
	if err != nil {
		t.Fatalf("read graph_projection xmin: %v", err)
	}
	return x
}
