package api

import "github.com/prometheus/client_golang/prometheus"

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
	reg.MustRegister(s.requests, s.durations, s.syncRuns, s.syncDuration, s.syncItems)
}
