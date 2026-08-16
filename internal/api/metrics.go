package api

import (
	"context"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/skillsync"
	"github.com/sunstoneinstitute/worklode/internal/store"
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
	s.cockpitProjections = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_cockpit_projection_requests_total",
		Help: "Project cockpit projection assembly attempts, by surface (api, web) and outcome (ok, not_found, error).",
	}, []string{"surface", "outcome"})
	s.navigations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_web_navigation_requests_total",
		Help: "Web UI navigation requests, by destination and outcome (ok, not_found, error).",
	}, []string{"destination", "outcome"})
	s.authzDecisions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_authz_decisions_total",
		Help: "Authorization decisions, by permission and outcome (allow, deny). A deny rate above zero on a permission nobody should be attempting is the signal worth alerting on.",
	}, []string{"permission", "outcome"})
	s.formSubmissions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_web_form_submissions_total",
		Help: "Web UI creation-form submissions, by form (task, deliverable) and outcome (created, invalid, forbidden, not_found, error).",
	}, []string{"form", "outcome"})
	s.docSyncRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_doc_sync_runs_total",
		Help: "Doc sync requests by result.",
	}, []string{"result"})
	s.docSyncDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "worklode_doc_sync_duration_seconds",
		Help:    "Doc sync request duration.",
		Buckets: []float64{0.05, 0.1, 0.5, 1, 5, 15},
	})
	s.docSyncDocs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_doc_sync_docs_total",
		Help: "Documents synced, by kind and outcome.",
	}, []string{"kind", "outcome"})
	s.docSyncForced = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "worklode_doc_sync_forced_total",
		Help: "Forced (--force) doc syncs accepted.",
	})
	s.localMerges = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_local_merge_reports_total",
		Help: "Tasks named in a local merge report, by result (advanced, duplicate, unknown_task). Steady 'duplicate' traffic is what a healthy webhook-plus-clone pair looks like; its absence means a reporter has stopped.",
	}, []string{"result"})
	s.listExpansions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_list_expansions_total",
		Help: "List endpoint requests that asked for an expansion, by endpoint (tasks, docs) and expansion (detail, body).",
	}, []string{"endpoint", "expansion"})
	// A distinct counter, not left to http_requests_total, because a seek is
	// the one admin-triggered write on this surface: it is the only way an
	// operator moves a subscriber's offsets backwards (025 §18), and how
	// often that happens is worth alerting on independently of request
	// volume. The GET reads beside it (listEvents, listEventSubscribers) are
	// ordinary reads with no derived outcome, so the generic HTTP middleware
	// (http_requests_total/http_request_duration_seconds) is judged
	// sufficient for them — see the eventbus package's
	// worklode_event_subscriber_lag and worklode_events_processed_total for
	// the domain-level visibility into the log itself.
	s.eventSubscriberSeeks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_event_subscriber_seeks_total",
		Help: "Admin seeks of a subscriber's offsets (POST /api/v1/event-subscribers/{name}/seek), by subscriber.",
	}, []string{"subscriber"})
	// The stream's own two instruments. http_requests_total cannot stand in
	// for either: a follow is one request that lasts hours, so it is counted
	// once at the end and its duration lands in http_request_duration_seconds
	// only when the client hangs up. How many follows are open right now, and
	// how much they are pushing, are the two questions an operator actually
	// asks about this route.
	s.eventStreamsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "worklode_event_streams_active",
		Help: "Open event-log follows (GET /api/v1/events/stream). Each holds a connection and a repeating horizon-bounded query.",
	})
	s.eventStreamEventsSent = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "worklode_event_stream_events_sent_total",
		Help: "Events pushed to event-log followers, summed across all open streams.",
	})
	// The horizon's position is a scrape-time fact, not something a handler
	// increments, so it is a collector rather than a gauge. It lives here
	// because this is where it gets registered: eventbus.NewMetrics, which
	// owns the per-subscriber lag gauge, has no caller yet.
	if s.st != nil {
		reg.MustRegister(&eventHorizonCollector{horizonID: s.st.EventLogHorizonID})
	}
	reg.MustRegister(s.requests, s.durations, s.syncRuns, s.syncDuration, s.syncItems, s.assignments,
		s.cockpitProjections, s.navigations, s.formSubmissions, s.authzDecisions,
		s.docSyncRuns, s.docSyncDuration, s.docSyncDocs, s.docSyncForced, s.localMerges,
		s.eventSubscriberSeeks, s.eventStreamsActive, s.eventStreamEventsSent, s.listExpansions)

	// Pre-initialise so alert expressions see 0, not no-data (as serve.go does
	// for the sweeper).
	s.syncRuns.WithLabelValues("ok")
	s.syncRuns.WithLabelValues("error")
	for _, action := range []string{"assign", "unassign", "start", "stop"} {
		s.assignments.WithLabelValues(action)
	}
	for _, result := range []string{
		store.LocalMergeAdvanced, store.LocalMergeDuplicate, store.LocalMergeUnknownTask,
	} {
		s.localMerges.WithLabelValues(result)
	}
	for _, surface := range []string{"api", "web"} {
		for _, outcome := range []string{"ok", "not_found", "error"} {
			s.cockpitProjections.WithLabelValues(surface, outcome)
		}
	}
	for _, destination := range []string{
		"home", "intake", "projects", "work", "reviews", "deliveries", "knowledge",
		"project_section", "asset", "deliverables", "deliverable_new", "task_new",
	} {
		for _, outcome := range []string{"ok", "not_found", "error", "rejected"} {
			s.navigations.WithLabelValues(destination, outcome)
		}
	}
	// Every declared permission, so a permission that is never exercised
	// reads as a flat zero rather than as no-data — the difference between
	// "nobody tried" and "we are not measuring".
	for perm := range grants {
		for _, outcome := range []string{"allow", "deny"} {
			s.authzDecisions.WithLabelValues(string(perm), outcome)
		}
	}
	for _, form := range []string{"task", "deliverable"} {
		for _, outcome := range []string{"created", "invalid", "forbidden", "not_found", "error"} {
			s.formSubmissions.WithLabelValues(form, outcome)
		}
	}
	s.docSyncRuns.WithLabelValues("ok")
	s.docSyncRuns.WithLabelValues("error")
}

// A follow that has gone quiet reads identically whether the log is quiet or
// the commit horizon is stuck behind a long-running transaction: the stream
// keeps heartbeating either way (025 §15). This gauge is the difference —
// flat while events are still being recorded means the horizon is held back,
// and pg_stat_activity is the next place to look.
var eventLogHorizonDesc = prometheus.NewDesc(
	"worklode_event_log_horizon_id",
	"Highest event id below the commit horizon (pg_snapshot_xmin) at scrape time. A log position, not a count.",
	nil, nil)

// eventHorizonCollector reads the horizon at scrape time, in the same mould as
// eventbus's lag collector: a bounded timeout, and an invalid metric on
// failure so a scrape error surfaces instead of a stale zero.
type eventHorizonCollector struct {
	horizonID func(context.Context) (int64, error)
}

func (c *eventHorizonCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- eventLogHorizonDesc
}

func (c *eventHorizonCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id, err := c.horizonID(ctx)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(eventLogHorizonDesc, err)
		return
	}
	ch <- prometheus.MustNewConstMetric(eventLogHorizonDesc, prometheus.GaugeValue, float64(id))
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

// recordLocalMerge records the result of one task named in a local merge
// report. Nil-safe: tests build a *server directly without initMetrics.
func (s *server) recordLocalMerge(result string) {
	if s.localMerges == nil {
		return
	}
	s.localMerges.WithLabelValues(result).Inc()
}

// cockpitOutcome classifies an assembleProjectCockpit error for the
// worklode_cockpit_projection_requests_total outcome label.
func cockpitOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, store.ErrNotFound) {
		return "not_found"
	}
	return "error"
}

// observeCockpitProjection records one attempted cockpit projection assembly,
// called exactly once per attempt from both the JSON API handler
// (surface="api") and the web project page (surface="web").
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeCockpitProjection(surface string, err error) {
	if s.cockpitProjections == nil {
		return
	}
	s.cockpitProjections.WithLabelValues(surface, cockpitOutcome(err)).Inc()
}

// observeNavigation records one web UI page request, by destination (see
// navWrap in web.go) and outcome ("ok", "not_found", "error", classified by
// navOutcome from the response status).
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeNavigation(destination, outcome string) {
	if s.navigations == nil {
		return
	}
	s.navigations.WithLabelValues(destination, outcome).Inc()
}

// formOutcome classifies a creation-form error for the
// worklode_web_form_submissions_total outcome label. Rejected input and a
// refused cross-origin POST are recorded by their handlers as "invalid" and
// "forbidden" — neither is an error here.
func formOutcome(err error) string {
	if err == nil {
		return "created"
	}
	if errors.Is(err, store.ErrNotFound) {
		return "not_found"
	}
	if errors.Is(err, store.ErrInvalidInput) {
		return "invalid"
	}
	return "error"
}

// observeAuthz records one authorization decision, called exactly once per
// guarded request from requirePerm (API) and webGuard (web). permPublic never
// reaches an enforcement point, so it never appears as a label value.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeAuthz(perm Permission, d Decision) {
	if s.authzDecisions == nil {
		return
	}
	outcome := "deny"
	if d.Allowed {
		outcome = "allow"
	}
	s.authzDecisions.WithLabelValues(string(perm), outcome).Inc()
}

// observeFormSubmission records one web creation-form submission, called
// exactly once per POST from webform.go.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeFormSubmission(form, outcome string) {
	if s.formSubmissions == nil {
		return
	}
	s.formSubmissions.WithLabelValues(form, outcome).Inc()
}

// observeEventSubscriberSeek records one successful admin seek of a
// subscriber's offsets. Called on success only, matching observeAssignment.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeEventSubscriberSeek(subscriber string) {
	if s.eventSubscriberSeeks == nil {
		return
	}
	s.eventSubscriberSeeks.WithLabelValues(subscriber).Inc()
}

// observeEventStreamOpen and observeEventStreamClose bracket one open
// follow; eventstream.go pairs them with a defer so a panicking handler still
// decrements.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeEventStreamOpen() {
	if s.eventStreamsActive == nil {
		return
	}
	s.eventStreamsActive.Inc()
}

func (s *server) observeEventStreamClose() {
	if s.eventStreamsActive == nil {
		return
	}
	s.eventStreamsActive.Dec()
}

// observeEventStreamSent records n events pushed to one follower, called once
// per flushed batch rather than once per event.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeEventStreamSent(n int) {
	if s.eventStreamEventsSent == nil {
		return
	}
	s.eventStreamEventsSent.Add(float64(n))
}

// observeListExpansion records one expanded list request. Nil-safe: tests
// build a *server directly without initMetrics.
func (s *server) observeListExpansion(endpoint, expansion string) {
	if s.listExpansions == nil {
		return
	}
	s.listExpansions.WithLabelValues(endpoint, expansion).Inc()
}

// observeDocSync records one sync request. Nil-safe: tests build a *server
// directly without initMetrics.
func (s *server) observeDocSync(results []store.DocSyncResult, forced bool, err error, d time.Duration) {
	if s.docSyncRuns == nil {
		return
	}
	s.docSyncDuration.Observe(d.Seconds())
	result := "ok"
	if err != nil {
		result = "error"
	}
	s.docSyncRuns.WithLabelValues(result).Inc()
	if forced && err == nil {
		s.docSyncForced.Inc()
	}
	for _, r := range results {
		s.docSyncDocs.WithLabelValues(r.Kind, r.Outcome).Inc()
	}
}
