package watcher

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the doc-lifecycle watcher's instrument. A nil *Metrics
// records nothing, so tests can pass nil.
type Metrics struct {
	actions *prometheus.CounterVec
}

// outcomes bounds the "outcome" label of worklode_watcher_actions_total
// (025 §15.7). rules bounds "rule" to the two labels Evaluate emits.
var (
	rules    = []string{ruleReviewOnSubmit, rulePlanOnAccept}
	outcomes = []string{"applied", "suppressed", "error"}
)

// NewMetrics registers the watcher counter on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		// worklode_watcher_actions_total{rule, outcome} — outcome is one of
		// applied|suppressed|error (spec 025 §15.7). Both labels are
		// bounded: rule is one of the two rule names Evaluate emits,
		// outcome one of the three above.
		actions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_watcher_actions_total",
			Help: "Doc-lifecycle watcher actions, by rule and outcome (applied, suppressed, error; spec 025 §15.7).",
		}, []string{"rule", "outcome"}),
	}
	reg.MustRegister(m.actions)

	// Pre-initialise every {rule, outcome} pair so alert expressions see 0
	// rather than no-data, as internal/api/metrics.go does for its
	// bounded-label counters.
	for _, rule := range rules {
		for _, outcome := range outcomes {
			m.actions.WithLabelValues(rule, outcome)
		}
	}
	return m
}

// Actions exposes the counter for test assertions.
func (m *Metrics) Actions() *prometheus.CounterVec {
	return m.actions
}

// Action records one watcher action outcome for rule. Exported — unlike
// internal/hooks's recorders, the executor that calls this lives in
// internal/api, a different package.
func (m *Metrics) Action(rule, outcome string) {
	if m == nil {
		return
	}
	m.actions.WithLabelValues(rule, outcome).Inc()
}
