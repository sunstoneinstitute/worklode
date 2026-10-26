package typesafe

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics instruments TypeSafe calls (WL-REQ-32). A nil *Metrics records
// nothing.
type Metrics struct {
	requests *prometheus.CounterVec
	duration prometheus.Histogram
}

// NewMetrics registers the TypeSafe instruments on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_typesafe_requests_total",
			Help: "TypeSafe System One calls by outcome: ok, error, or unconfigured (no API key, no request sent).",
		}, []string{"outcome"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "worklode_typesafe_request_seconds",
			Help:    "TypeSafe System One request duration.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

func (m *Metrics) observe(outcome string, d time.Duration) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(outcome).Inc()
	if outcome != "unconfigured" {
		m.duration.Observe(d.Seconds())
	}
}
