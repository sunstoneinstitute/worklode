package archive

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds the archive instruments (WL-REQ-1894, WL-REQ-32). A nil
// *Metrics records nothing.
type Metrics struct {
	errors prometheus.Counter
}

// NewMetrics registers the archive instruments on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_events_archive_errors_total",
			Help: "Failed exports of an event to the event archive.",
		}),
	}
	reg.MustRegister(m.errors)
	return m
}

func (m *Metrics) exportError() {
	if m == nil {
		return
	}
	m.errors.Inc()
}
