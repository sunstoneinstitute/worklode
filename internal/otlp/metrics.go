package otlp

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds the otlp package's instruments (spec 071 §3, WL-SPEC-22). A
// nil *Metrics records nothing, so decode and forward can run without one.
type Metrics struct {
	ingest       *prometheus.CounterVec
	records      *prometheus.CounterVec
	forward      *prometheus.CounterVec
	queueDropped prometheus.Counter
}

// NewMetrics registers the otlp instruments on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		ingest: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_otlp_ingest_total",
			Help: "POST /otlp/v1/logs requests, by outcome.",
		}, []string{"outcome"}),
		records: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_otlp_records_total",
			Help: "Decoded OTLP log records, by outcome (stored or unattributed).",
		}, []string{"outcome"}),
		forward: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_otlp_forward_total",
			Help: "OTLP batches forwarded to the upstream otel-gateway, by outcome.",
		}, []string{"outcome"}),
		queueDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_otlp_forward_queue_dropped_total",
			Help: "OTLP batches dropped because the forward queue was full.",
		}),
	}
	reg.MustRegister(m.ingest, m.records, m.forward, m.queueDropped)
	return m
}

// Ingest counts one /otlp/v1/logs request. outcome is one of "ok",
// "unsupported_media", "too_large", "bad_request", "forbidden",
// "store_error".
func (m *Metrics) Ingest(outcome string) {
	if m == nil {
		return
	}
	m.ingest.WithLabelValues(outcome).Inc()
}

// Records counts n decoded log records. outcome is "stored",
// "unattributed" (a record with no task id), or "unknown_task" (a record
// naming a task the backbone does not have).
func (m *Metrics) Records(outcome string, n int) {
	if m == nil || n <= 0 {
		return
	}
	m.records.WithLabelValues(outcome).Add(float64(n))
}

// Forward counts one forwarded batch. outcome is "ok", "client_error",
// "server_error" or "network".
func (m *Metrics) Forward(outcome string) {
	if m == nil {
		return
	}
	m.forward.WithLabelValues(outcome).Inc()
}

// QueueDropped counts one batch dropped because the forward queue was full.
func (m *Metrics) QueueDropped() {
	if m == nil {
		return
	}
	m.queueDropped.Inc()
}
