package mcp

import "github.com/prometheus/client_golang/prometheus"

// Tool call outcomes (WL-REQ-1825). The label set is closed so cardinality
// stays bounded.
const (
	outcomeOK              = "ok"
	outcomeInvalid         = "invalid"
	outcomeProjectRequired = "project_required"
	outcomeConfirmProject  = "confirm_project"
	outcomeNotFound        = "not_found"
	outcomeRefused         = "refused"
	outcomeConflict        = "conflict"
	outcomeError           = "error"
)

// Metrics holds the MCP server's instruments (WL-REQ-1825). A nil *Metrics
// records nothing.
type Metrics struct {
	toolCalls *prometheus.CounterVec
	oauth     *prometheus.CounterVec
}

// NewMetrics registers both MCP counters on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		toolCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_mcp_tool_calls_total",
			Help: "MCP tool calls, by tool and outcome (ok, invalid, project_required, confirm_project, not_found, refused, conflict, error).",
		}, []string{"tool", "outcome"}),
		oauth: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_mcp_oauth_requests_total",
			Help: "MCP OAuth requests, by endpoint (metadata, authorize, callback, consent, token) and outcome (ok, not_found, or the OAuth error code).",
		}, []string{"endpoint", "outcome"}),
	}
	reg.MustRegister(m.toolCalls, m.oauth)
	return m
}

func (m *Metrics) toolCall(tool, outcome string) {
	if m != nil {
		m.toolCalls.WithLabelValues(tool, outcome).Inc()
	}
}
