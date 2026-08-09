package api

import (
	"errors"
	"fmt"
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
