package watcher_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/watcher"
)

// TestMetricsNilSafe: a nil *Metrics is what package-level callers get
// before initMetrics runs (or in tests that build things directly), and
// must not panic.
func TestMetricsNilSafe(t *testing.T) {
	var m *watcher.Metrics
	m.Action("review-on-submit", "applied")
}

func TestMetricsAction(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := watcher.NewMetrics(reg)

	m.Action("review-on-submit", "applied")
	m.Action("plan-on-accept", "suppressed")
	m.Action("plan-on-accept", "suppressed")

	for _, tc := range []struct {
		rule, outcome string
		want          float64
	}{
		{"review-on-submit", "applied", 1},
		{"plan-on-accept", "suppressed", 2},
		// Pre-initialised, never recorded: reads as 0, not no-data.
		{"plan-on-accept", "error", 0},
		{"review-on-submit", "suppressed", 0},
	} {
		got := testutil.ToFloat64(m.Actions().WithLabelValues(tc.rule, tc.outcome))
		if got != tc.want {
			t.Fatalf("actions{%s,%s} = %v, want %v", tc.rule, tc.outcome, got, tc.want)
		}
	}
}
