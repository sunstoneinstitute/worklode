package mdblocks

import "github.com/prometheus/client_golang/prometheus"

// Metrics counts body normalisations (WL-REQ-1867). A nil *Metrics records
// nothing, so Normalize through a nil receiver costs only the conversion.
type Metrics struct {
	normalize *prometheus.CounterVec
}

// subjects bounds the kind label: the three subjects whose bodies are
// normalised.
var subjects = []string{"doc", "rule", "task"}

// NewMetrics registers the instruments on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{normalize: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_mdblocks_normalize_total",
		Help: "Body writes normalised, by subject (doc, rule, task) and whether normalising changed the text.",
	}, []string{"kind", "changed"})}
	reg.MustRegister(m.normalize)
	for _, k := range subjects {
		m.normalize.WithLabelValues(k, "true")
		m.normalize.WithLabelValues(k, "false")
	}
	return m
}

// Normalize is Normalize(md), counted under kind ("doc", "rule" or "task").
func (m *Metrics) Normalize(kind string, md []byte) []byte {
	out := Normalize(md)
	if m != nil {
		changed := "false"
		if string(out) != string(md) {
			changed = "true"
		}
		m.normalize.WithLabelValues(kind, changed).Inc()
	}
	return out
}
