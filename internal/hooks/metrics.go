package hooks

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the webhook instruments, shared by the GitHub and Flux
// handlers. A nil *Metrics records nothing, so tests can pass nil.
type Metrics struct {
	events        *prometheus.CounterVec
	truncatedPush prometheus.Counter
	branchResolve *prometheus.CounterVec
	replay        *prometheus.CounterVec
}

// NewMetrics registers the webhook counters and the reconcile replay counter
// on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		events: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_webhook_events_total",
			Help: "Webhook deliveries by source, event type, and result.",
		}, []string{"source", "event", "result"}),
		// Unlabelled on purpose: this answers "has it ever happened", and the
		// log line carries repo, ref and the sha range for the one that did.
		truncatedPush: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_webhook_push_truncated_total",
			Help: "Push deliveries whose commits array did not reach the pushed head.",
		}),
		// branchResolve counts GitHub API calls made to turn a release's
		// target_commitish branch name into a commit sha. outcome is one of
		// "resolved", "unknown" (branch does not exist), "error", or
		// "skipped" (no App configured).
		branchResolve: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_github_branch_resolve_total",
			Help: "GitHub branch-to-commit resolutions attempted by the release webhook, by outcome.",
		}, []string{"outcome"}),
		// replay counts stored events reconcile's replayer walked. outcome is
		// one of "replayed", "still_unmapped", "dry_run", or "error" — a
		// bounded set, one value per candidate event per run.
		replay: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_reconcile_replay_events_total",
			Help: "Stored webhook events processed by reconcile replay, by outcome.",
		}, []string{"outcome"}),
	}
	reg.MustRegister(m.events, m.truncatedPush, m.branchResolve, m.replay)
	return m
}

// Events exposes the counter for test assertions.
func (m *Metrics) Events() *prometheus.CounterVec {
	return m.events
}

// TruncatedPush exposes the counter for test assertions.
func (m *Metrics) TruncatedPush() prometheus.Counter {
	return m.truncatedPush
}

// BranchResolve exposes the counter for test assertions.
func (m *Metrics) BranchResolve() *prometheus.CounterVec {
	return m.branchResolve
}

// ReplayEvents exposes the counter for test assertions.
func (m *Metrics) ReplayEvents() *prometheus.CounterVec {
	return m.replay
}

func (m *Metrics) truncatedPushDelivery() {
	if m == nil {
		return
	}
	m.truncatedPush.Inc()
}

func (m *Metrics) event(source, event, result string) {
	if m == nil {
		return
	}
	m.events.WithLabelValues(source, event, result).Inc()
}

func (m *Metrics) branchResolved(outcome string) {
	if m == nil {
		return
	}
	m.branchResolve.WithLabelValues(outcome).Inc()
}

func (m *Metrics) replayOutcome(outcome string) {
	if m == nil {
		return
	}
	m.replay.WithLabelValues(outcome).Inc()
}
