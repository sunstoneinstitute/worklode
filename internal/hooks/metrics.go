package hooks

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the webhook instruments, shared by the GitHub, Flux and
// catalog handlers. A nil *Metrics records nothing, so tests can pass nil.
type Metrics struct {
	events           *prometheus.CounterVec
	truncatedPush    prometheus.Counter
	branchResolve    *prometheus.CounterVec
	replay           *prometheus.CounterVec
	approvals        *prometheus.CounterVec
	artifactEvidence *prometheus.CounterVec
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
		// approvals counts approval rows the GitHub ingest wrote. action is
		// one of "opened" (a PR materialized an awaiting row), "resolved" (a
		// review decided one), "reopened" (a re-request put one back in the
		// queue), "rebound" (a synchronize moved an open row's
		// subject_revision to the new head), "candidate" (a synchronize
		// after a decided review filed a new awaiting row for the new head),
		// or "impact_opened" (a dependency's revision change opened an
		// impact row on this entity).
		approvals: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_approvals_ingest_total",
			Help: "Approval-relevant actions taken by the GitHub webhook ingest, by action.",
		}, []string{"action"}),
		// artifactEvidence counts evidence rows the artifact-evidence ingests
		// (catalog, ci, pipeline, ...) wrote. source is the ingest that filed
		// the row; state and entity_kind are bounded by the artifact_evidence
		// CHECK constraints.
		artifactEvidence: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_artifact_evidence_total",
			Help: "Evidence rows written by the artifact-evidence ingests, by source, artifact state, and the kind of entity that declared it.",
		}, []string{"source", "state", "entity_kind"}),
	}
	reg.MustRegister(m.events, m.truncatedPush, m.branchResolve, m.replay,
		m.approvals, m.artifactEvidence)
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

// ApprovalsIngest exposes the counter for test assertions.
func (m *Metrics) ApprovalsIngest() *prometheus.CounterVec {
	return m.approvals
}

// ArtifactEvidence exposes the counter for test assertions.
func (m *Metrics) ArtifactEvidence() *prometheus.CounterVec {
	return m.artifactEvidence
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

func (m *Metrics) approvalIngest(action string) {
	if m == nil {
		return
	}
	m.approvals.WithLabelValues(action).Inc()
}

// catalogEvidenceFiled counts one apply's evidence rows — from a live
// delivery or from reconcile's replay of a stored one, which write the same
// rows and so are counted the same way. source is the ingest that ran the
// apply (catalog, ci, pipeline, ...).
func (m *Metrics) catalogEvidenceFiled(source string, res catalogResult) {
	if m == nil {
		return
	}
	for _, e := range res.Written {
		m.artifactEvidence.WithLabelValues(source, res.State, e.Kind).Inc()
	}
}
