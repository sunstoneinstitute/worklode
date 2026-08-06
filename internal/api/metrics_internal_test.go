package api

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/skillsync"
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
