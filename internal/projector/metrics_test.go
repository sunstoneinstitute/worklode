package projector_test

import (
	"database/sql"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/projector"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// newMetricsProjector is newProjector's counterpart for tests that assert on
// counter movement: it wires a real *projector.Metrics on its own registry
// instead of the nil metrics newProjector passes. Assertions read the
// registry directly (findMetric et al.) rather than through Metrics, which
// exposes nothing test-only — matching internal/eventbus/metrics.go.
func newMetricsProjector(t *testing.T) (*store.Store, *projector.Projector, *fakeGraphServer, *prometheus.Registry) {
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
	return s, projector.New(s, graphserver.New(srv.URL, nil), m, 100), f, reg
}

func TestMetricsSuccessfulRunIncrementsOkAndProjects(t *testing.T) {
	s, p, _, reg := newMetricsProjector(t)
	createTask(t, s, "m1", "alpha", "wire the metrics")

	n, err := p.RunOnce(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v; want 1, nil", n, err)
	}

	if got := counterValue(t, reg, "worklode_graph_projection_runs_total", "result", "ok"); got != 1 {
		t.Errorf("runs_total{ok} = %v, want 1", got)
	}
	if got := counterValue(t, reg, "worklode_graph_projection_runs_total", "result", "error"); got != 0 {
		t.Errorf("runs_total{error} = %v, want 0 (pre-initialised, not no-data)", got)
	}
	if got := counterValue(t, reg, "worklode_graph_projection_projects_total", "", ""); got != 1 {
		t.Errorf("projects_total = %v, want 1", got)
	}
	if got := histogramCount(t, reg, "worklode_graph_projection_duration_seconds"); got != 1 {
		t.Errorf("duration_seconds observation count = %d, want 1", got)
	}
}

func TestMetricsFailingRunIncrementsErrorLeavesProjects(t *testing.T) {
	s, p, f, reg := newMetricsProjector(t)
	createTask(t, s, "m2", "alpha", "unlucky")

	f.setFail(true)
	if _, err := p.RunOnce(t.Context()); err == nil {
		t.Fatal("RunOnce against a failing endpoint returned nil error")
	}

	if got := counterValue(t, reg, "worklode_graph_projection_runs_total", "result", "error"); got != 1 {
		t.Errorf("runs_total{error} = %v, want 1", got)
	}
	if got := counterValue(t, reg, "worklode_graph_projection_runs_total", "result", "ok"); got != 0 {
		t.Errorf("runs_total{ok} = %v, want 0", got)
	}
	if got := counterValue(t, reg, "worklode_graph_projection_projects_total", "", ""); got != 0 {
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

// findMetric returns family's only sample when label is empty (an
// unlabelled counter/histogram), or the sample whose label=value when it
// isn't. Mirrors internal/eventbus/loop_test.go's helper of the same shape.
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
			if label == "" {
				return m
			}
			for _, lp := range m.GetLabel() {
				if lp.GetName() == label && lp.GetValue() == value {
					return m
				}
			}
		}
	}
	return nil
}

// counterValue reads one counter family's value from g, filtered by
// label=value (pass "", "" for an unlabelled counter).
func counterValue(t *testing.T, g prometheus.Gatherer, family, label, value string) float64 {
	t.Helper()
	m := findMetric(t, g, family, label, value)
	if m == nil {
		return 0
	}
	return m.GetCounter().GetValue()
}

// histogramCount reads an unlabelled histogram family's observation count
// from g.
func histogramCount(t *testing.T, g prometheus.Gatherer, family string) uint64 {
	t.Helper()
	m := findMetric(t, g, family, "", "")
	if m == nil {
		return 0
	}
	return m.GetHistogram().GetSampleCount()
}
