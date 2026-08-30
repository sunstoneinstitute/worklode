package api

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/overview"
	"github.com/sunstoneinstitute/worklode/internal/skillsync"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestObserveSkillSync(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	s.observeSkillSync(skillsync.Summary{Synced: 3, Changed: 1, Embedded: 1}, nil, 250*time.Millisecond)
	s.observeSkillSync(skillsync.Summary{Synced: 2, Deleted: 4}, errors.New("boom"), time.Second)

	for _, tc := range []struct {
		result string
		want   float64
	}{{"ok", 1}, {"error", 1}} {
		if got := testutil.ToFloat64(s.syncRuns.WithLabelValues(tc.result)); got != tc.want {
			t.Fatalf("syncRuns{%s} = %v, want %v", tc.result, got, tc.want)
		}
	}
	for _, tc := range []struct {
		action string
		want   float64
	}{{"synced", 5}, {"changed", 1}, {"embedded", 1}, {"deleted", 4}} {
		if got := testutil.ToFloat64(s.syncItems.WithLabelValues(tc.action)); got != tc.want {
			t.Fatalf("syncItems{%s} = %v, want %v", tc.action, got, tc.want)
		}
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var count uint64
	for _, mf := range mfs {
		if mf.GetName() == "worklode_skill_sync_duration_seconds" {
			count = mf.GetMetric()[0].GetHistogram().GetSampleCount()
		}
	}
	if count != 2 {
		t.Fatalf("syncDuration observations = %d, want 2 (the error pass must be timed too)", count)
	}
}

func TestObserveAssignment(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	s.observeAssignment("assign")
	s.observeAssignment("assign")
	s.observeAssignment("unassign")
	s.observeAssignment("start")
	s.observeAssignment("stop")

	for _, tc := range []struct {
		action string
		want   float64
	}{{"assign", 2}, {"unassign", 1}, {"start", 1}, {"stop", 1}} {
		if got := testutil.ToFloat64(s.assignments.WithLabelValues(tc.action)); got != tc.want {
			t.Fatalf("assignments{%s} = %v, want %v", tc.action, got, tc.want)
		}
	}
}

// TestObserveAssignmentNilSafe checks a *server built without initMetrics
// (as tests in this package do) does not panic when a handler calls
// observeAssignment.
func TestObserveAssignmentNilSafe(t *testing.T) {
	t.Parallel()
	s := &server{}
	s.observeAssignment("assign")
}

func TestCockpitOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "ok"},
		{"not found", store.ErrNotFound, "not_found"},
		{"wrapped not found", fmt.Errorf("get project: %w", store.ErrNotFound), "not_found"},
		{"other", errors.New("boom"), "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cockpitOutcome(tt.err); got != tt.want {
				t.Fatalf("cockpitOutcome(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// TestObserveCockpitProjectionRegistersMetric asserts
// worklode_cockpit_projection_requests_total is registered (and, thanks to
// pre-initialisation, gatherable with zero observations) as soon as
// initMetrics runs.
func TestObserveCockpitProjectionRegistersMetric(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "worklode_cockpit_projection_requests_total" {
			found = true
		}
	}
	if !found {
		t.Fatalf("worklode_cockpit_projection_requests_total not registered")
	}
}

// TestObserveCockpitProjection covers the three outcomes across both
// surfaces, with no id label present anywhere.
func TestObserveCockpitProjection(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	s.observeCockpitProjection("api", nil)
	s.observeCockpitProjection("web", nil)
	s.observeCockpitProjection("api", store.ErrNotFound)
	s.observeCockpitProjection("web", errors.New("boom"))

	for _, tc := range []struct {
		surface, outcome string
		want             float64
	}{
		{"api", "ok", 1}, {"web", "ok", 1},
		{"api", "not_found", 1}, {"web", "not_found", 0},
		{"api", "error", 0}, {"web", "error", 1},
	} {
		if got := testutil.ToFloat64(s.cockpitProjections.WithLabelValues(tc.surface, tc.outcome)); got != tc.want {
			t.Fatalf("cockpitProjections{surface=%s,outcome=%s} = %v, want %v", tc.surface, tc.outcome, got, tc.want)
		}
	}
}

// TestObserveCockpitProjectionNilSafe checks a *server built without
// initMetrics (as tests in this package do) does not panic when a handler
// calls observeCockpitProjection.
func TestObserveCockpitProjectionNilSafe(t *testing.T) {
	t.Parallel()
	s := &server{}
	s.observeCockpitProjection("api", nil)
}

// TestObserveNavigation covers one successful route (home), one missing
// project section (project_section/not_found), and one asset response
// (asset/ok) — the three cases the plan calls out — plus registration and
// bounded labels with no project or task id anywhere.
func TestObserveNavigation(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	s.observeNavigation("home", "ok")
	s.observeNavigation("project_section", "not_found")
	s.observeNavigation("asset", "ok")

	for _, tc := range []struct {
		destination, outcome string
		want                 float64
	}{
		{"home", "ok", 1},
		{"project_section", "not_found", 1},
		{"asset", "ok", 1},
		{"home", "not_found", 0},
		{"projects", "ok", 0},
	} {
		if got := testutil.ToFloat64(s.navigations.WithLabelValues(tc.destination, tc.outcome)); got != tc.want {
			t.Fatalf("navigations{destination=%s,outcome=%s} = %v, want %v", tc.destination, tc.outcome, got, tc.want)
		}
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "worklode_web_navigation_requests_total" {
			found = true
		}
	}
	if !found {
		t.Fatalf("worklode_web_navigation_requests_total not registered")
	}
}

// TestObserveNavigationNilSafe checks a *server built without initMetrics
// (as tests in this package do) does not panic when a handler calls
// observeNavigation.
func TestObserveNavigationNilSafe(t *testing.T) {
	t.Parallel()
	s := &server{}
	s.observeNavigation("home", "ok")
}

func TestObserveHomeRender(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	s.observeHomeRender(homeModeActor)
	s.observeHomeRender(homeModeActor)
	s.observeHomeRender(homeModeOpen)

	for _, tc := range []struct {
		mode string
		want float64
	}{
		{homeModeActor, 2},
		{homeModeOpen, 1},
		// Pre-initialised, so an instance nobody has landed empty on reads as
		// a flat zero rather than as no-data.
		{homeModeEmpty, 0},
	} {
		if got := testutil.ToFloat64(s.homeRenders.WithLabelValues(tc.mode)); got != tc.want {
			t.Fatalf("homeRenders{mode=%s} = %v, want %v", tc.mode, got, tc.want)
		}
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var modes []string
	for _, mf := range mfs {
		if mf.GetName() != "worklode_web_home_renders_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				modes = append(modes, l.GetName()+"="+l.GetValue())
			}
		}
	}
	// The label is bounded to exactly the three modes: never a project id.
	want := []string{"mode=actor", "mode=empty", "mode=open"}
	slices.Sort(modes)
	if !slices.Equal(modes, want) {
		t.Fatalf("worklode_web_home_renders_total series = %v, want %v", modes, want)
	}
}

// TestObserveHomeRenderNilSafe checks a *server built without initMetrics
// (as tests in this package do) does not panic when homePage calls
// observeHomeRender.
func TestObserveHomeRenderNilSafe(t *testing.T) {
	t.Parallel()
	s := &server{}
	s.observeHomeRender(homeModeOpen)
}

func TestObserveListExpansion(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	s.observeListExpansion("docs", "body")
	s.observeListExpansion("docs", "body")

	if got := testutil.ToFloat64(s.listExpansions.WithLabelValues("docs", "body")); got != 2 {
		t.Errorf("listExpansions{docs,body} = %v, want 2", got)
	}

	// Nil-safe: a server built without initMetrics must not panic.
	(&server{}).observeListExpansion("docs", "body")
}

// The horizon gauge is what tells "the log is quiet" apart from "the commit
// horizon is stuck". It reads at scrape time, and a failed read must surface
// as a scrape error rather than as a plausible zero.
func TestEventHorizonCollector(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	reg.MustRegister(&eventHorizonCollector{
		horizonID: func(context.Context) (int64, error) { return 4711, nil },
	})

	const want = `# HELP worklode_event_log_horizon_id Highest event id below the commit horizon (pg_snapshot_xmin) at scrape time. A log position, not a count.
# TYPE worklode_event_log_horizon_id gauge
worklode_event_log_horizon_id 4711
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "worklode_event_log_horizon_id"); err != nil {
		t.Fatalf("gather: %v", err)
	}

	failing := prometheus.NewRegistry()
	failing.MustRegister(&eventHorizonCollector{
		horizonID: func(context.Context) (int64, error) { return 0, errors.New("horizon boom") },
	})
	if _, err := failing.Gather(); err == nil {
		t.Fatal("a failed horizon read gathered cleanly, want the scrape to error rather than report a stale zero")
	} else if !strings.Contains(err.Error(), "horizon boom") {
		t.Fatalf("gather error = %v, want it to name the underlying failure", err)
	}
}

// The collector is registered only when the server has a store, so the
// storeless *server the tests in this package build still initialises metrics.
func TestEventHorizonCollectorSkippedWithoutStore(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == "worklode_event_log_horizon_id" {
			t.Fatal("worklode_event_log_horizon_id registered without a store to query")
		}
	}
}

func TestRecordLocalMerge(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == "worklode_event_log_horizon_id" {
			t.Fatal("worklode_event_log_horizon_id registered without a store to query")
		}
	}
}

// TestRecordLocalMergeNilSafe: handlers call this on a server a test may have
// built without initMetrics.
func TestRecordLocalMergeNilSafe(t *testing.T) {
	t.Parallel()
	(&server{}).recordLocalMerge(store.LocalMergeAdvanced)
}

// TestObserveDeleteCounts: every entity/op/outcome combination is
// pre-initialised to zero (044 §6 wants justification_required legible as a
// flat zero, not as no-data), and observeDelete increments the named one.
func TestObserveDeleteCounts(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	for _, entity := range deleteEntities {
		for _, op := range deleteOps {
			for _, outcome := range deleteOutcomes {
				if got := testutil.ToFloat64(s.deletes.WithLabelValues(entity, op, outcome)); got != 0 {
					t.Fatalf("deletes{%s,%s,%s} = %v before any request, want a pre-initialised 0",
						entity, op, outcome, got)
				}
			}
		}
	}

	s.observeDelete(entityTask, opDelete, deleteJustificationRequired)
	s.observeDelete(entityTask, opDelete, deleteOK)
	s.observeDelete(entityTask, opUndelete, deleteOK)
	s.observeDelete(entityDoc, opDelete, deleteNotFound)

	for _, tc := range []struct {
		entity, op, outcome string
		want                float64
	}{
		{entityTask, opDelete, deleteJustificationRequired, 1},
		{entityTask, opDelete, deleteOK, 1},
		{entityTask, opUndelete, deleteOK, 1},
		{entityDoc, opDelete, deleteNotFound, 1},
		{entityDoc, opUndelete, deleteOK, 0},
	} {
		got := testutil.ToFloat64(s.deletes.WithLabelValues(tc.entity, tc.op, tc.outcome))
		if got != tc.want {
			t.Fatalf("deletes{%s,%s,%s} = %v, want %v", tc.entity, tc.op, tc.outcome, got, tc.want)
		}
	}
}

// TestDeleteOutcomeClassifies: only a missing row is not_found; an
// already-deleted row (ErrInvalidInput) is an error, keeping 044 §6's outcome
// set to the four values it names.
func TestDeleteOutcomeClassifies(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"success", nil, deleteOK},
		{"missing row", fmt.Errorf("task WL-9: %w", store.ErrNotFound), deleteNotFound},
		{"already deleted", fmt.Errorf("already deleted: %w", store.ErrInvalidInput), deleteError},
		{"anything else", errors.New("boom"), deleteError},
	} {
		if got := deleteOutcome(tc.err); got != tc.want {
			t.Errorf("%s: deleteOutcome = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestObserveDeleteNilSafe: the handlers call it on a server a test may have
// built without initMetrics.
func TestObserveDeleteNilSafe(t *testing.T) {
	t.Parallel()
	(&server{}).observeDelete(entityTask, opDelete, deleteOK)
}

// TestRegisterRoutesLeavesNavMetricsUninitialised pins the position-independence
// the nav zero-series depend on (WL-186). The series are minted from the set of
// destinations registration collected, so they must be minted *after*
// registration finishes — from NewServer, not from inside registerRoutes, where
// a route added below the call would silently lose its zero-series.
func TestRegisterRoutesLeavesNavMetricsUninitialised(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	if _, err := s.registerRoutes(reg); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if len(s.navDestinations) == 0 {
		t.Fatal("registration collected no nav destinations")
	}
	if got := countNavSeries(t, reg); got != 0 {
		t.Fatalf("registerRoutes minted %d nav series; it must leave that to the caller, so a route registered anywhere in the function is covered", got)
	}

	s.initNavMetrics()
	unique := map[string]bool{}
	for _, d := range s.navDestinations {
		unique[d] = true
	}
	if got, want := countNavSeries(t, reg), len(unique)*4; got != want {
		t.Fatalf("nav series after initNavMetrics = %d, want %d (one per destination per outcome)", got, want)
	}
}

func countNavSeries(t *testing.T, g prometheus.Gatherer) int {
	t.Helper()
	mfs, err := g.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == "worklode_web_navigation_requests_total" {
			return len(mf.GetMetric())
		}
	}
	return 0
}

func TestOverviewOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "ok"},
		{"no graph", overview.ErrNoGraph, "no_graph"},
		{"wrapped no graph", fmt.Errorf("gap report: %w", overview.ErrNoGraph), "no_graph"},
		{"other", errors.New("boom"), "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := overviewOutcome(tt.err); got != tt.want {
				t.Fatalf("overviewOutcome(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// TestObserveOverviewRead: every read/outcome pair the handlers can produce
// is counted, and the pre-initialised series are there before the first
// request — except no_graph on the three backbone-authoritative reads, which
// cannot return ErrNoGraph and must not carry a permanently flat series.
func TestObserveOverviewRead(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	s.observeOverviewRead(readOverview, overviewOutcome(nil))
	s.observeOverviewRead(readDrift, overviewOutcome(overview.ErrNoGraph))
	s.observeOverviewRead(readDrift, overviewOutcome(overview.ErrNoGraph))
	s.observeOverviewRead(readGaps, overviewOutcome(errors.New("sparql said no")))

	for _, tc := range []struct {
		read, outcome string
		want          float64
	}{
		{readOverview, "ok", 1},
		{readDrift, "no_graph", 2},
		{readGaps, "error", 1},
		{readFrontier, "ok", 0}, // pre-initialised, never observed
	} {
		got := testutil.ToFloat64(s.overviewReads.WithLabelValues(tc.read, tc.outcome))
		if got != tc.want {
			t.Errorf("overviewReads{%s,%s} = %v, want %v", tc.read, tc.outcome, got, tc.want)
		}
	}

	labels := gatheredLabelPairs(t, reg, "worklode_overview_reads_total", "read", "outcome")
	for _, read := range []string{readOverview, readFrontier, readCriticalPath} {
		if slices.Contains(labels, read+"/"+overviewNoGraph) {
			t.Errorf("worklode_overview_reads_total has a %s/no_graph series; that read is backbone-authoritative and cannot return ErrNoGraph", read)
		}
	}
	for _, read := range []string{readDrift, readGaps} {
		if !slices.Contains(labels, read+"/"+overviewNoGraph) {
			t.Errorf("worklode_overview_reads_total is missing %s/no_graph; a graph-less instance must read as a flat count, not as no-data", read)
		}
	}
}

func TestDeriveOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		res  model.DeriveResult
		err  error
		want string
	}{
		{"written", model.DeriveResult{Bytes: 12}, nil, "written"},
		{"skipped", model.DeriveResult{Skipped: true}, nil, "skipped"},
		{"refused empty", model.DeriveResult{}, derive.ErrWouldEmptyGraph, "refused_empty"},
		{"wrapped refused empty", model.DeriveResult{},
			fmt.Errorf("observed/deploy: %w (the deriver produced no triples)", derive.ErrWouldEmptyGraph),
			"refused_empty"},
		{"error", model.DeriveResult{}, errors.New("PUT failed"), "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveOutcome(tt.res, tt.err); got != tt.want {
				t.Fatalf("deriveOutcome(%+v, %v) = %q, want %q", tt.res, tt.err, got, tt.want)
			}
		})
	}
}

// TestObserveDeriveRun: one observation per deriver, and every source/outcome
// pair is pre-initialised so an instance nobody has run the derivers on reads
// as a flat zero rather than as no-data.
func TestObserveDeriveRun(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	s.observeDeriveRun(deriveDeploy, deriveOutcome(model.DeriveResult{Bytes: 40}, nil))
	s.observeDeriveRun(derivePRAffects, deriveOutcome(model.DeriveResult{Skipped: true}, nil))
	s.observeDeriveRun(derivePRAffects, deriveOutcome(model.DeriveResult{}, derive.ErrWouldEmptyGraph))

	for _, tc := range []struct {
		source, outcome string
		want            float64
	}{
		{deriveDeploy, "written", 1},
		{deriveDeploy, "error", 0}, // pre-initialised, never observed
		{derivePRAffects, "skipped", 1},
		{derivePRAffects, "refused_empty", 1},
	} {
		got := testutil.ToFloat64(s.deriveRuns.WithLabelValues(tc.source, tc.outcome))
		if got != tc.want {
			t.Errorf("deriveRuns{%s,%s} = %v, want %v", tc.source, tc.outcome, got, tc.want)
		}
	}

	labels := gatheredLabelPairs(t, reg, "worklode_derive_runs_total", "source", "outcome")
	if want := len(deriveSources) * len(deriveOutcomes); len(labels) != want {
		t.Fatalf("worklode_derive_runs_total has %d series (%v), want %d — every source/outcome pair pre-initialised",
			len(labels), labels, want)
	}
}

// TestObserveOverviewNilSafe checks a *server built without initMetrics (as
// tests in this package do) does not panic on either new instrument.
func TestObserveOverviewNilSafe(t *testing.T) {
	t.Parallel()
	s := &server{}
	s.observeOverviewRead(readDrift, overviewNoGraph)
	s.observeDeriveRun(deriveDeploy, deriveWritten)
}

// gatheredLabelPairs returns "<first value>/<second value>" for every series
// of a metric family, sorted. Looked up by label name rather than by
// position: Gather sorts label pairs alphabetically, which is not the order
// the CounterVec declares them in.
func gatheredLabelPairs(t *testing.T, reg *prometheus.Registry, name, first, second string) []string {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var out []string
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			byName := map[string]string{}
			for _, lp := range m.GetLabel() {
				byName[lp.GetName()] = lp.GetValue()
			}
			out = append(out, byName[first]+"/"+byName[second])
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s not registered", name)
	}
	slices.Sort(out)
	return out
}
