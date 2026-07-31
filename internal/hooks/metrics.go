package hooks

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the webhook instruments, shared by the GitHub and Flux
// handlers. A nil *Metrics records nothing, so tests can pass nil.
type Metrics struct {
	events *prometheus.CounterVec
}

// NewMetrics registers the webhook counter on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{events: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_webhook_events_total",
		Help: "Webhook deliveries by source, event type, and result.",
	}, []string{"source", "event", "result"})}
	reg.MustRegister(m.events)
	return m
}

// Events exposes the counter for test assertions.
func (m *Metrics) Events() *prometheus.CounterVec {
	return m.events
}

func (m *Metrics) event(source, event, result string) {
	if m == nil {
		return
	}
	m.events.WithLabelValues(source, event, result).Inc()
}
