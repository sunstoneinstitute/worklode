package projector

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the projector loop's instruments (spec 022). A nil *Metrics
// records nothing, so RunOnce can call its methods unconditionally, as this
// package's tests that pass nil do.
type Metrics struct {
	runs        *prometheus.CounterVec // worklode_graph_projection_runs_total{result}
	projects    prometheus.Counter     // worklode_graph_projection_projects_total
	duration    prometheus.Histogram   // worklode_graph_projection_duration_seconds
	failures    prometheus.Counter     // worklode_graph_projection_project_failures_total
	quarantined prometheus.Gauge       // worklode_graph_projection_quarantined_projects
	deleted     prometheus.Counter     // worklode_graph_projection_graphs_deleted_total
}

// NewMetrics registers the projector's instruments on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_graph_projection_runs_total",
			Help: "RunOnce calls by outcome (ok, error).",
		}, []string{"result"}),
		projects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_graph_projection_projects_total",
			Help: "Project graphs successfully PUT to graph-server.",
		}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "worklode_graph_projection_duration_seconds",
			Help:    "Time one RunOnce call took, success or failure.",
			Buckets: []float64{0.01, 0.1, 1, 10},
		}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_graph_projection_project_failures_total",
			Help: "Per-project projection attempts that failed and quarantined the project.",
		}),
		quarantined: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "worklode_graph_projection_quarantined_projects",
			Help: "Projects currently owing a projection after a failure, as of the last run.",
		}),
		deleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_graph_projection_graphs_deleted_total",
			Help: "Declared graphs removed from graph-server because their document was tombstoned.",
		}),
	}
	reg.MustRegister(m.runs, m.projects, m.duration, m.failures, m.quarantined, m.deleted)

	// Pre-initialise both series so alert expressions see 0, not no-data.
	m.runs.WithLabelValues("ok")
	m.runs.WithLabelValues("error")

	return m
}

// recordRun records the outcome and duration of one RunOnce call. result is
// "ok" or "error" — nothing else, so the label stays bounded.
func (m *Metrics) recordRun(result string, d time.Duration) {
	if m == nil {
		return
	}
	m.runs.WithLabelValues(result).Inc()
	m.duration.Observe(d.Seconds())
}

// recordProject records one project graph successfully PUT to graph-server.
func (m *Metrics) recordProject() {
	if m == nil {
		return
	}
	m.projects.Inc()
}

// recordGraphDeleted records one declared graph removed because its document
// was tombstoned (044 §4). Only an actual removal counts: a delete that finds
// the graph already gone is the steady state of a reconciler re-issuing it,
// not an event, and counting it would turn one deletion into a rate.
func (m *Metrics) recordGraphDeleted() {
	if m == nil {
		return
	}
	m.deleted.Inc()
}

// recordProjectFailure records one project that failed to project and was
// quarantined. No project label: 022 §8 allows only bounded label values and
// the project set is not closed. Which project is stuck is in the slog.Error
// line beside this call and in graph_projection_failures.
func (m *Metrics) recordProjectFailure() {
	if m == nil {
		return
	}
	m.failures.Inc()
}

// setQuarantined publishes how many projects are in quarantine after a run.
// A sustained non-zero value is the signal that one project is persistently
// failing while the rest of the batch keeps flowing — the case that used to
// show up only as runs_total{result="error"} climbing with projects_total
// flat, which no longer distinguishes it from a broken batch.
func (m *Metrics) setQuarantined(n int) {
	if m == nil {
		return
	}
	m.quarantined.Set(float64(n))
}
