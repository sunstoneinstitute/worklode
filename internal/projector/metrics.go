package projector

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the projector loop's instruments (spec 022). A nil *Metrics
// records nothing, so RunOnce can call its methods unconditionally, as this
// package's tests that pass nil do.
type Metrics struct {
	runs     *prometheus.CounterVec // worklode_graph_projection_runs_total{result}
	projects prometheus.Counter     // worklode_graph_projection_projects_total
	duration prometheus.Histogram   // worklode_graph_projection_duration_seconds
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
	}
	reg.MustRegister(m.runs, m.projects, m.duration)

	// Pre-initialise both series so alert expressions see 0, not no-data.
	m.runs.WithLabelValues("ok")
	m.runs.WithLabelValues("error")

	return m
}

// Runs exposes the counter for test assertions.
func (m *Metrics) Runs() *prometheus.CounterVec {
	return m.runs
}

// Projects exposes the counter for test assertions.
func (m *Metrics) Projects() prometheus.Counter {
	return m.projects
}

// Duration exposes the histogram for test assertions.
func (m *Metrics) Duration() prometheus.Histogram {
	return m.duration
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
