package embed

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics instruments outbound embedding calls. A nil *Metrics records
// nothing, so bare OpenAI literals (tests, CLI paths) stay uninstrumented.
type Metrics struct {
	requests *prometheus.CounterVec
	duration prometheus.Histogram
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_embed_requests_total",
			Help: "Outbound embedding API calls by result. One per Embed call, however many texts it batches.",
		}, []string{"result"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "worklode_embed_request_duration_seconds",
			Help:    "Outbound embedding API call duration.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

// Requests exposes the counter for test assertions.
func (m *Metrics) Requests() *prometheus.CounterVec {
	return m.requests
}

func (m *Metrics) observe(err error, d time.Duration) {
	if m == nil {
		return
	}
	m.duration.Observe(d.Seconds())
	result := "ok"
	if err != nil {
		result = "error"
	}
	m.requests.WithLabelValues(result).Inc()
}
