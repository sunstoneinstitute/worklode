package api

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/skillsync"
)

// initMetrics creates and registers the server-owned instruments (HTTP
// middleware and skill sync) on reg.
func (s *server) initMetrics(reg prometheus.Registerer) {
	s.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests served, by method, route pattern, and status code.",
	}, []string{"method", "route", "code"})
	s.durations = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration, by method and route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	s.syncRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_skill_sync_runs_total",
		Help: "Skill sync passes by result.",
	}, []string{"result"})
	s.syncDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "worklode_skill_sync_duration_seconds",
		Help:    "Skill sync pass duration.",
		Buckets: []float64{0.1, 0.5, 1, 5, 15, 60, 300},
	})
	s.syncItems = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_skill_sync_items_total",
		Help: "Skills touched by sync passes, by action.",
	}, []string{"action"})
	s.assignments = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_task_assignments_total",
		Help: "Task assignment actions, by action (assign, unassign, start, stop).",
	}, []string{"action"})
	reg.MustRegister(s.requests, s.durations, s.syncRuns, s.syncDuration, s.syncItems, s.assignments)

	// Pre-initialise so alert expressions see 0, not no-data (as serve.go does
	// for the sweeper).
	s.syncRuns.WithLabelValues("ok")
	s.syncRuns.WithLabelValues("error")
}

// observeSkillSync records one sync pass, called from both syncOnce
// (background) and the admin sync handler. A partial failure still carries a
// summary of what landed before the error, so items are recorded on both
// paths (spec 022 §4).
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeSkillSync(sum skillsync.Summary, err error, d time.Duration) {
	if s.syncDuration == nil {
		return
	}
	s.syncDuration.Observe(d.Seconds())
	result := "ok"
	if err != nil {
		result = "error"
	}
	s.syncRuns.WithLabelValues(result).Inc()
	for action, n := range map[string]int{
		"synced":   sum.Synced,
		"changed":  sum.Changed,
		"embedded": sum.Embedded,
		"deleted":  sum.Deleted,
	} {
		s.syncItems.WithLabelValues(action).Add(float64(n))
	}
}

// observeAssignment records one assignment action (assign, unassign, start,
// stop). Called on success only.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeAssignment(action string) {
	if s.assignments == nil {
		return
	}
	s.assignments.WithLabelValues(action).Inc()
}
