package mdrender

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds the render cache's instruments. A nil *Metrics records
// nothing, so a Cache built without a registerer — every test, and every
// *server a test constructs directly — costs nothing.
type Metrics struct {
	lookups   *prometheus.CounterVec
	renders   *prometheus.CounterVec
	evictions prometheus.Counter
	bytes     prometheus.Gauge
}

// lookupResults and renderOutcomes bound the two label values. Both are
// closed sets fixed at compile time; neither ever carries a task id, a body
// or anything else an author controls.
var (
	lookupResults  = []string{"hit", "miss"}
	renderOutcomes = []string{outcomeOK, outcomeOversize, outcomeFallback}
)

// NewMetrics registers the cache's instruments on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		lookups: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_mdrender_cache_lookups_total",
			Help: "Task-body render cache lookups, by result (hit, miss).",
		}, []string{"result"}),
		renders: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_mdrender_renders_total",
			Help: "Task-body renders actually performed, by outcome (ok, oversize, fallback). A cache hit performs none.",
		}, []string{"outcome"}),
		evictions: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_mdrender_cache_evictions_total",
			Help: "Entries evicted from the task-body render cache to stay inside its byte and entry bounds.",
		}),
		bytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "worklode_mdrender_cache_bytes",
			Help: "Rendered HTML currently held by the task-body render cache.",
		}),
	}
	reg.MustRegister(m.lookups, m.renders, m.evictions, m.bytes)

	// Pre-initialise every series so alert expressions see 0 rather than
	// no-data, as internal/api/metrics.go does for its bounded-label
	// counters.
	for _, r := range lookupResults {
		m.lookups.WithLabelValues(r)
	}
	for _, o := range renderOutcomes {
		m.renders.WithLabelValues(o)
	}
	return m
}

// Lookups, Renders, Evictions and Bytes expose the instruments for test
// assertions.
func (m *Metrics) Lookups() *prometheus.CounterVec { return m.lookups }
func (m *Metrics) Renders() *prometheus.CounterVec { return m.renders }
func (m *Metrics) Evictions() prometheus.Counter   { return m.evictions }
func (m *Metrics) Bytes() prometheus.Gauge         { return m.bytes }

func (m *Metrics) lookup(result string) {
	if m == nil {
		return
	}
	m.lookups.WithLabelValues(result).Inc()
}

func (m *Metrics) render(outcome string) {
	if m == nil {
		return
	}
	m.renders.WithLabelValues(outcome).Inc()
}

func (m *Metrics) evicted(n int) {
	if m == nil || n == 0 {
		return
	}
	m.evictions.Add(float64(n))
}

func (m *Metrics) setBytes(n int) {
	if m == nil {
		return
	}
	m.bytes.Set(float64(n))
}
