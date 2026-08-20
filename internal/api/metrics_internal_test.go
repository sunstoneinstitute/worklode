package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/skillsync"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestObserveSkillSync(t *testing.T) {
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
	s := &server{}
	s.observeAssignment("assign")
}

func TestCockpitOutcome(t *testing.T) {
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
	s := &server{}
	s.observeCockpitProjection("api", nil)
}

// TestObserveNavigation covers one successful route (home), one missing
// project section (project_section/not_found), and one asset response
// (asset/ok) — the three cases the plan calls out — plus registration and
// bounded labels with no project or task id anywhere.
func TestObserveNavigation(t *testing.T) {
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
	s := &server{}
	s.observeNavigation("home", "ok")
}

func TestObserveListExpansion(t *testing.T) {
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
	(&server{}).recordLocalMerge(store.LocalMergeAdvanced)
}

// TestObserveDeleteCounts: every entity/op/outcome combination is
// pre-initialised to zero (044 §6 wants justification_required legible as a
// flat zero, not as no-data), and observeDelete increments the named one.
func TestObserveDeleteCounts(t *testing.T) {
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
	(&server{}).observeDelete(entityTask, opDelete, deleteOK)
}
