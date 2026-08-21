package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Option configures Open.
type Option func(*Store)

// WithMetrics registers the store's Prometheus instruments on reg: claim and
// lease counters, the active-lease gauge, and database/sql pool stats.
// Without this option the store records nothing.
func WithMetrics(reg prometheus.Registerer) Option {
	return func(s *Store) {
		s.metrics = newStoreMetrics(reg)
		reg.MustRegister(collectors.NewDBStatsCollector(s.db, "worklode"))
		reg.MustRegister(&leaseCollector{db: s.db, now: s.Now})
	}
}

// storeMetrics holds the store's domain instruments. All methods are nil-safe
// so call sites need no guards on stores opened without WithMetrics.
type storeMetrics struct {
	claims           *prometheus.CounterVec
	renewals         *prometheus.CounterVec
	releases         *prometheus.CounterVec
	expiries         prometheus.Counter
	sweeperRuns      *prometheus.CounterVec
	projectWorkReads *prometheus.CounterVec
	docOps           *prometheus.CounterVec
	docTasksMinted   prometheus.Counter
}

func newStoreMetrics(reg prometheus.Registerer) *storeMetrics {
	m := &storeMetrics{
		claims: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_claims_total",
			Help: "Claim attempts by op and outcome. claim-next's internal claim attempts also count under op=claim.",
		}, []string{"op", "outcome"}),
		renewals: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_lease_renewals_total",
			Help: "Lease renewals by outcome.",
		}, []string{"outcome"}),
		releases: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_lease_releases_total",
			Help: "Lease releases by outcome.",
		}, []string{"outcome"}),
		expiries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_lease_expiries_total",
			Help: "Leases closed by the expiry sweeper.",
		}),
		sweeperRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_lease_sweeper_runs_total",
			Help: "Lease sweeper runs by result.",
		}, []string{"result"}),
		projectWorkReads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_project_work_reads_total",
			Help: "ListProjectWorkFacts reads by outcome.",
		}, []string{"outcome"}),
		docOps: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_doc_operations_total",
			Help: "Design-document mutations by op (create|update|accept|submit|revise|edges|delete|undelete) and outcome.",
		}, []string{"op", "outcome"}),
		docTasksMinted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_doc_plan_tasks_minted_total",
			Help: "Tasks minted across all plan-document accepts (025 §9.2).",
		}),
	}
	reg.MustRegister(m.claims, m.renewals, m.releases, m.expiries, m.sweeperRuns, m.projectWorkReads, m.docOps, m.docTasksMinted)
	// Pre-initialise both sweeper series so alert expressions see 0, not
	// no-data, on a server whose sweeper has not ticked yet.
	m.sweeperRuns.WithLabelValues("ok")
	m.sweeperRuns.WithLabelValues("error")
	return m
}

func (m *storeMetrics) claim(op, outcome string) {
	if m == nil {
		return
	}
	m.claims.WithLabelValues(op, outcome).Inc()
}

func (m *storeMetrics) renew(outcome string) {
	if m == nil {
		return
	}
	m.renewals.WithLabelValues(outcome).Inc()
}

func (m *storeMetrics) release(outcome string) {
	if m == nil {
		return
	}
	m.releases.WithLabelValues(outcome).Inc()
}

func (m *storeMetrics) expire(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.expiries.Add(float64(n))
}

// sweeperRun records one lease-sweeper tick by result. The label key is
// "result", not "outcome": this is plain operational success/failure, not a
// domain sentinel (022 §3).
func (m *storeMetrics) sweeperRun(err error) {
	if m == nil {
		return
	}
	m.sweeperRuns.WithLabelValues(outcome(err)).Inc()
}

// projectWorkRead records one ListProjectWorkFacts call by outcome. Never
// labeled by project or task id — those are unbounded, and this metric only
// needs to answer "is the bulk reader healthy," not "for which project."
func (m *storeMetrics) projectWorkRead(err error) {
	if m == nil {
		return
	}
	m.projectWorkReads.WithLabelValues(outcome(err)).Inc()
}

// docOp records one document mutation by op and outcome. op is the caller's
// fixed verb — create|update|accept|submit|revise|edges|delete|undelete, the
// enumeration the Help string above is the contract for (022 §8) — never a doc
// id or project, which are unbounded. Adding a verb means adding it there too.
func (m *storeMetrics) docOp(op string, err error) {
	if m == nil {
		return
	}
	m.docOps.WithLabelValues(op, outcome(err)).Inc()
}

// planTasksMinted adds n to worklode_doc_plan_tasks_minted_total, the tasks
// minted by one plan accept. n <= 0 records nothing; a successful plan accept
// always mints at least one task (PlanTasks refuses an empty set).
func (m *storeMetrics) planTasksMinted(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.docTasksMinted.Add(float64(n))
}

// claimOutcome maps a Claim error to its metric label. Everything outside the
// spec'd sentinel set (including ErrBadTransition) is "error".
func claimOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrLeased):
		return "leased"
	case errors.Is(err, ErrBlocked):
		return "blocked"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	default:
		return "error"
	}
}

// outcome maps any error to ok/error, for renewals and releases.
func outcome(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

var leasesActiveDesc = prometheus.NewDesc(
	"worklode_leases_active",
	"Active (unreleased, unexpired) leases, counted at scrape time.",
	nil, nil)

// leaseCollector counts active leases at scrape time, against the store's
// clock. On query failure it emits an invalid metric (surfacing a scrape
// error) rather than a stale zero.
type leaseCollector struct {
	db  *sql.DB
	now func() time.Time
}

func (c *leaseCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- leasesActiveDesc
}

func (c *leaseCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var n float64
	err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM leases WHERE released_at IS NULL AND expires_at > $1`,
		c.now().UTC(),
	).Scan(&n)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(leasesActiveDesc, err)
		return
	}
	ch <- prometheus.MustNewConstMetric(leasesActiveDesc, prometheus.GaugeValue, n)
}
