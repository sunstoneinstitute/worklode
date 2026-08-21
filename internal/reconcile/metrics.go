package reconcile

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds engine 2's instruments. A nil *Metrics records nothing, so
// tests and any caller without a registerer can leave Options.Metrics unset.
type Metrics struct {
	candidates   *prometheus.CounterVec
	commitChecks *prometheus.CounterVec
	repairs      *prometheus.CounterVec
	repoErrors   prometheus.Counter
}

// NewMetrics registers engine 2's counters on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		// candidates counts the poll candidates a run examined. Every
		// candidate lands on exactly one outcome per run: "gather_error"
		// (its repo's GitHub reads failed, so no facts were gathered for
		// it), "dry_run", "error" (the apply transaction did not land),
		// "repaired", or "clean" — a bounded set of five.
		candidates: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_reconcile_poll_candidates_total",
			Help: "Poll candidates examined by reconcile engine 2, by outcome.",
		}, []string{"outcome"}),
		// commitChecks counts the branch-membership questions a run resolved,
		// by how: "checked" spent a GitHub request, "skipped_landed" read the
		// answer out of main_commits, "skipped_settled" is a merged PR's head
		// sha whose merge sha already landed (spec 013 §2.2 — the org-wide run
		// is rate-limit bound, so the skip ratio is the thing to watch).
		commitChecks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_reconcile_poll_commit_checks_total",
			Help: "Branch-membership questions resolved by reconcile engine 2, by how they were resolved.",
		}, []string{"outcome"}),
		// repairs counts the facts an applied run repaired, so a run that
		// touched one candidate with twenty stale commits is distinguishable
		// from one that touched twenty candidates. fact is "pr" or "commit".
		repairs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_reconcile_poll_repairs_total",
			Help: "Facts repaired by an applied reconcile poll run, by fact kind.",
		}, []string{"fact"}),
		// Unlabelled on purpose: repo is unbounded cardinality, and this
		// answers "is the gather phase degrading" — the log line carries the
		// repo and the error for the one that failed.
		repoErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_reconcile_poll_repo_errors_total",
			Help: "Repos whose GitHub gather phase failed during a poll run.",
		}),
	}
	reg.MustRegister(m.candidates, m.commitChecks, m.repairs, m.repoErrors)
	return m
}

// Candidates exposes the counter for test assertions.
func (m *Metrics) Candidates() *prometheus.CounterVec {
	return m.candidates
}

// CommitChecks exposes the counter for test assertions.
func (m *Metrics) CommitChecks() *prometheus.CounterVec {
	return m.commitChecks
}

// Repairs exposes the counter for test assertions.
func (m *Metrics) Repairs() *prometheus.CounterVec {
	return m.repairs
}

// RepoErrors exposes the counter for test assertions.
func (m *Metrics) RepoErrors() prometheus.Counter {
	return m.repoErrors
}

// candidateOutcome adds n candidates to outcome. n may be 0: the series is
// still created, so a scrape distinguishes "no gather errors" from "this
// build never reports gather errors".
func (m *Metrics) candidateOutcome(outcome string, n int) {
	if m == nil {
		return
	}
	m.candidates.WithLabelValues(outcome).Add(float64(n))
}

// commitCheck records how one candidate sha's branch membership was resolved.
func (m *Metrics) commitCheck(outcome string) {
	if m == nil {
		return
	}
	m.commitChecks.WithLabelValues(outcome).Inc()
}

func (m *Metrics) repaired(fact string, n int) {
	if m == nil {
		return
	}
	m.repairs.WithLabelValues(fact).Add(float64(n))
}

func (m *Metrics) repoError() {
	if m == nil {
		return
	}
	m.repoErrors.Inc()
}
