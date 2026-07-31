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
	claims   *prometheus.CounterVec
	renewals *prometheus.CounterVec
	releases *prometheus.CounterVec
	expiries prometheus.Counter
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
	}
	reg.MustRegister(m.claims, m.renewals, m.releases, m.expiries)
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
