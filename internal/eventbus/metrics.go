package eventbus

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Metrics holds the subscriber-loop instruments (spec 025 §15.7). A nil
// *Metrics records nothing, so a loop can run without them.
type Metrics struct {
	processed *prometheus.CounterVec   // worklode_events_processed_total{subscriber,type,outcome}
	batchDur  *prometheus.HistogramVec // worklode_event_batch_duration_seconds{subscriber}
}

// NewMetrics registers the eventbus instruments on reg, plus the lag gauge —
// a custom collector in the worklode_leases_active mould: it queries
// per-subscriber lag at scrape time (2 s timeout) and emits an invalid metric
// on failure rather than a stale zero. st may be nil to register the counters
// alone (the lag gauge needs a store to query).
func NewMetrics(reg prometheus.Registerer, st *store.Store) *Metrics {
	m := &Metrics{
		processed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_events_processed_total",
			Help: "Events handled by a subscriber loop, by subscriber, event type and outcome.",
		}, []string{"subscriber", "type", "outcome"}),
		batchDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "worklode_event_batch_duration_seconds",
			Help:    "Time to handle and ack one batch of events, by subscriber.",
			Buckets: []float64{0.001, 0.01, 0.1, 1, 10},
		}, []string{"subscriber"}),
	}
	reg.MustRegister(m.processed, m.batchDur)
	if st != nil {
		reg.MustRegister(&lagCollector{store: st})
	}
	return m
}

// event counts one handled event. outcome is one of applied, suppressed or
// error (025 §15.7); the caller passes nothing else.
func (m *Metrics) event(subscriber, typ, outcome string) {
	if m == nil {
		return
	}
	if !KnownType(typ) {
		typ = "other" // 025 §15.7: bounded label — the log also carries dotted vendor types.
	}
	m.processed.WithLabelValues(subscriber, typ, outcome).Inc()
}

// batch observes the time one batch took to handle and ack.
func (m *Metrics) batch(subscriber string, seconds float64) {
	if m == nil {
		return
	}
	m.batchDur.WithLabelValues(subscriber).Observe(seconds)
}

var subscriberLagDesc = prometheus.NewDesc(
	"worklode_event_subscriber_lag",
	"Events below the commit horizon not yet acked, per subscriber, counted at scrape time.",
	[]string{"subscriber"}, nil)

// lagCollector reads per-subscriber lag at scrape time. On query failure it
// emits an invalid metric (surfacing a scrape error) rather than a stale zero.
type lagCollector struct {
	store *store.Store
}

func (c *lagCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- subscriberLagDesc
}

func (c *lagCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lags, err := c.store.EventSubscriberLags(ctx)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(subscriberLagDesc, err)
		return
	}
	for _, l := range lags {
		ch <- prometheus.MustNewConstMetric(subscriberLagDesc, prometheus.GaugeValue, float64(l.Lag), l.Name)
	}
}
