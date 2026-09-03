package indexer

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Metrics holds the convergence loop's instruments (040 §10). A nil *Metrics
// records nothing, so an Indexer built without a registry still runs.
type Metrics struct {
	chunks        *prometheus.GaugeVec
	withoutVector prometheus.Gauge
	stale         *prometheus.GaugeVec
	reembed       *prometheus.CounterVec
	convergence   prometheus.Histogram
}

// outcomes bounds the "outcome" label of worklode_index_reembed_total. Only
// two of §10's three values can occur here: a subject either re-indexes or
// fails, and "empty" belongs to search.
var outcomes = []string{"ok", "error"}

// NewMetrics registers the index instruments on reg. Both label values are
// bounded — subject_kind by index_chunks' CHECK (§5), outcome by the pair
// above — so no id ever reaches a label (022).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		chunks: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "worklode_index_chunks",
			Help: "Chunk rows in the corpus index, by subject kind (spec 040 §10).",
		}, []string{"subject_kind"}),
		withoutVector: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "worklode_index_chunks_without_vector",
			Help: "Chunk rows with no embedding: an instance with no provider, or one mid-re-embed after a provider change.",
		}),
		stale: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "worklode_index_subjects_stale",
			Help: "Subjects still stale after a convergence pass, by kind. Should return to zero every pass; a floor above zero means a subject fails to index repeatedly.",
		}, []string{"subject_kind"}),
		reembed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_index_reembed_total",
			Help: "Subjects re-indexed by the convergence loop, by kind and outcome (ok, error).",
		}, []string{"subject_kind", "outcome"}),
		convergence: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "worklode_index_convergence_duration_seconds",
			Help: "Duration of one full convergence pass over all subject kinds.",
			// A pass over an unchanged corpus is three cheap queries; a full
			// re-embed after a provider change runs for minutes.
			Buckets: []float64{0.01, 0.1, 1, 5, 15, 60, 300, 900},
		}),
	}
	reg.MustRegister(m.chunks, m.withoutVector, m.stale, m.reembed, m.convergence)

	// Pre-initialise every bounded series so an alert expression sees 0
	// rather than no-data before the first pass.
	for _, kind := range kinds {
		m.chunks.WithLabelValues(kind)
		m.stale.WithLabelValues(kind)
		for _, outcome := range outcomes {
			m.reembed.WithLabelValues(kind, outcome)
		}
	}
	return m
}

// Reembed counts one subject's re-index attempt.
func (m *Metrics) Reembed(kind, outcome string) {
	if m == nil {
		return
	}
	m.reembed.WithLabelValues(kind, outcome).Inc()
}

// Convergence records how long one full pass took.
func (m *Metrics) Convergence(d time.Duration) {
	if m == nil {
		return
	}
	m.convergence.Observe(d.Seconds())
}

// Counts sets the index-size gauges. A kind with no rows is set to zero
// rather than left at its last value, so a dropped corpus is visible.
func (m *Metrics) Counts(c store.IndexCounts) {
	if m == nil {
		return
	}
	for _, kind := range kinds {
		m.chunks.WithLabelValues(kind).Set(float64(c.ByKind[kind]))
	}
	m.withoutVector.Set(float64(c.WithoutVector))
}

// Stale sets the still-stale gauge for one kind.
func (m *Metrics) Stale(kind string, n int) {
	if m == nil {
		return
	}
	m.stale.WithLabelValues(kind).Set(float64(n))
}
