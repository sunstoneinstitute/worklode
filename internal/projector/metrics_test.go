package projector_test

import (
	"database/sql"
	"net/http/httptest"
	"testing"
	"time"

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
	if got := counterValue(t, reg, "worklode_graph_projection_project_failures_total", "", ""); got != 1 {
		t.Errorf("project_failures_total = %v, want 1", got)
	}
	if got := gaugeValue(t, reg, "worklode_graph_projection_quarantined_projects"); got != 1 {
		t.Errorf("quarantined_projects = %v, want 1", got)
	}

	// The gauge is a level, not a counter: recovery must bring it back down,
	// which is what makes a *sustained* non-zero value the alertable signal.
	f.setFail(false)
	if _, err := p.RunOnce(t.Context()); err != nil {
		t.Fatalf("recovery RunOnce: %v", err)
	}
	if got := gaugeValue(t, reg, "worklode_graph_projection_quarantined_projects"); got != 0 {
		t.Errorf("quarantined_projects after recovery = %v, want 0", got)
	}
	if got := counterValue(t, reg, "worklode_graph_projection_project_failures_total", "", ""); got != 1 {
		t.Errorf("project_failures_total after recovery = %v, want 1 (counters do not go down)", got)
	}
}

// TestMetricsGaugeCountsUntouchedQuarantine covers the branch the recovery
// case above does not: a quarantined project whose backoff has not elapsed is
// not attempted at all this run, and the gauge must still report it — the
// gauge is the number of projects owing a projection, not the number the last
// run happened to touch.
func TestMetricsGaugeCountsUntouchedQuarantine(t *testing.T) {
	s, p, f, reg := newMetricsProjector(t)
	ctx := t.Context()
	base := time.Now().UTC()
	p.SetClock(func() time.Time { return base })
	createTask(t, s, "m5", "alpha", "poison")
	store.AwaitCommitHorizon(t, s) // the checkpoint may only pass a settled batch

	f.setFail(true)
	for i := 1; i <= 2; i++ { // attempt 2 sets a retryBase wait
		if _, err := p.RunOnce(ctx); err == nil {
			t.Fatalf("run %d against a failing endpoint returned nil error", i)
		}
	}

	f.setFail(false)
	if n, err := p.RunOnce(ctx); n != 0 || err != nil {
		t.Fatalf("run inside the backoff = %d, %v; want 0, nil", n, err)
	}
	if got := gaugeValue(t, reg, "worklode_graph_projection_quarantined_projects"); got != 1 {
		t.Errorf("quarantined_projects = %v, want 1 (still owed, just not due)", got)
	}
	if got := counterValue(t, reg, "worklode_graph_projection_project_failures_total", "", ""); got != 2 {
		t.Errorf("project_failures_total = %v, want 2", got)
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

// gaugeValue reads an unlabelled gauge family's value from g.
func gaugeValue(t *testing.T, g prometheus.Gatherer, family string) float64 {
	t.Helper()
	m := findMetric(t, g, family, "", "")
	if m == nil {
		return 0
	}
	return m.GetGauge().GetValue()
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

// TestMetricsCountsOnlyRealGraphDeletions covers the counter added with 044
// §4's graph removal: the delete is re-issued on every pass, so counting the
// 404s would turn one deletion into a rate.
func TestMetricsCountsOnlyRealGraphDeletions(t *testing.T) {
	s, p, _, reg := newMetricsProjector(t)
	ctx := t.Context()
	if err := s.EnsureActor(ctx, "author", "human", "Author"); err != nil {
		t.Fatalf("ensure actor: %v", err)
	}

	var docID int64
	_, _, err := s.RecordEvent(ctx, "cli", "m-del1", "doc.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			d, cerr := store.CreateDoc(tx, time.Now().UTC(), store.DocInput{
				Project: "alpha", Kind: "spec", Number: 4, Slug: "004-counted",
				Body:      "---\nstatus: draft\n---\n# Spec 4 — Counted\n",
				CreatedBy: "author",
			}, eventID)
			if cerr != nil {
				return cerr
			}
			docID = d.ID
			return nil
		})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, err := p.RunOnce(ctx); err != nil {
		t.Fatalf("project the live doc: %v", err)
	}
	if got := counterValue(t, reg, "worklode_graph_projection_graphs_deleted_total", "", ""); got != 0 {
		t.Errorf("graphs_deleted_total before any delete = %v, want 0", got)
	}

	_, _, err = s.RecordEvent(ctx, "cli", "m-del1-delete", "doc.deleted", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.DeleteDoc(tx, time.Now().UTC(), docID, "author", "wrong number", eventID)
		})
	if err != nil {
		t.Fatalf("delete doc: %v", err)
	}
	if _, err := p.RunOnce(ctx); err != nil {
		t.Fatalf("project after the delete: %v", err)
	}
	if got := counterValue(t, reg, "worklode_graph_projection_graphs_deleted_total", "", ""); got != 1 {
		t.Fatalf("graphs_deleted_total after the delete = %v, want 1", got)
	}

	// Another pass re-issues the delete against a graph that is already gone.
	createTask(t, s, "m-del1-dirty", "alpha", "dirty the project again")
	if _, err := p.RunOnce(ctx); err != nil {
		t.Fatalf("re-projection: %v", err)
	}
	if got := counterValue(t, reg, "worklode_graph_projection_graphs_deleted_total", "", ""); got != 1 {
		t.Errorf("graphs_deleted_total after a re-issued delete = %v, want 1", got)
	}
}
