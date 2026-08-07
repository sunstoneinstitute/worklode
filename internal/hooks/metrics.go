package hooks

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the webhook instruments, shared by the GitHub and Flux
// handlers. A nil *Metrics records nothing, so tests can pass nil.
type Metrics struct {
	events        *prometheus.CounterVec
	truncatedPush prometheus.Counter
}

// NewMetrics registers the webhook counters on reg.
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
	}
	reg.MustRegister(m.events, m.truncatedPush)
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
