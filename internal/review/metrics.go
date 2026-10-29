package review

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the worklode_review_* instruments of WL-REQ-1327. A nil
// *Metrics records nothing. Every label is bounded by a CHECK or an enum:
// subject by reviews_subject_known, client by review_rounds_client_known,
// intent by review_threads_intent_known, verdict by
// review_rounds_verdict_known, reviewer_kind by the two values below.
type Metrics struct {
	rounds          *prometheus.CounterVec
	threads         *prometheus.CounterVec
	openThreads     *prometheus.GaugeVec
	questionLatency prometheus.Histogram
	timeToVerdict   prometheus.Histogram
	verdicts        *prometheus.CounterVec
}

var (
	subjects      = []string{"doc", "rule", "change"}
	clients       = []string{"crit", "web", "cli", "agent"}
	intents       = []string{"note", "change_request", "question"}
	verdicts      = []string{"approved", "changes_requested"}
	reviewerKinds = []string{"human", "agent"}
)

// NewMetrics registers the review instruments on reg and pre-initialises
// every bounded label combination, so a dashboard sees 0 rather than no data.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		rounds: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_review_rounds_total",
			Help: "Review rounds submitted, by review subject and client (WL-REQ-1327).",
		}, []string{"subject", "client"}),
		threads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_review_threads_total",
			Help: "Review threads opened, bare notes included, by intent (WL-REQ-1327).",
		}, []string{"intent"}),
		openThreads: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "worklode_review_open_threads",
			Help: "Open review threads, by subject, as counted after the last thread write (WL-REQ-1327).",
		}, []string{"subject"}),
		questionLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "worklode_review_question_latency_seconds",
			Help:    "Seconds from a question thread's opening to the author's first reply (WL-REQ-1327).",
			Buckets: []float64{5, 15, 60, 300, 900, 3600},
		}),
		timeToVerdict: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "worklode_review_time_to_verdict_seconds",
			Help:    "Seconds from a review's opening to a reviewer round with a verdict (WL-REQ-1327).",
			Buckets: []float64{300, 1800, 7200, 86400, 604800},
		}),
		verdicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_review_verdicts_total",
			Help: "Reviewer verdicts, by verdict and reviewer kind (WL-REQ-1327).",
		}, []string{"verdict", "reviewer_kind"}),
	}
	reg.MustRegister(m.rounds, m.threads, m.openThreads, m.questionLatency, m.timeToVerdict, m.verdicts)
	for _, s := range subjects {
		m.openThreads.WithLabelValues(s)
		for _, c := range clients {
			m.rounds.WithLabelValues(s, c)
		}
	}
	for _, i := range intents {
		m.threads.WithLabelValues(i)
	}
	for _, v := range verdicts {
		for _, k := range reviewerKinds {
			m.verdicts.WithLabelValues(v, k)
		}
	}
	return m
}

// Round counts one submitted round.
func (m *Metrics) Round(subject, client string) {
	if m == nil {
		return
	}
	m.rounds.WithLabelValues(subject, client).Inc()
}

// Thread counts one opened thread.
func (m *Metrics) Thread(intent string) {
	if m == nil {
		return
	}
	m.threads.WithLabelValues(intent).Inc()
}

// SetOpenThreads sets the open-thread gauge from a count per subject; a
// subject the map leaves out is 0.
func (m *Metrics) SetOpenThreads(counts map[string]int) {
	if m == nil {
		return
	}
	for _, s := range subjects {
		m.openThreads.WithLabelValues(s).Set(float64(counts[s]))
	}
}

// QuestionAnswered observes the time from a question to its first author reply.
func (m *Metrics) QuestionAnswered(latency time.Duration) {
	if m == nil {
		return
	}
	m.questionLatency.Observe(latency.Seconds())
}

// Verdict counts one reviewer verdict and observes the time since the review
// opened. actorKind is actors.kind; WL-REQ-1327 bounds the label to human and
// agent, so a service reviewer counts as agent.
func (m *Metrics) Verdict(verdict, actorKind string, sinceOpened time.Duration) {
	if m == nil {
		return
	}
	kind := "agent"
	if actorKind == "human" {
		kind = "human"
	}
	m.verdicts.WithLabelValues(verdict, kind).Inc()
	m.timeToVerdict.Observe(sinceOpened.Seconds())
}
