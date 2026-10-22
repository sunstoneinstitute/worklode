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
	toolCalls   *prometheus.CounterVec
	oauth       *prometheus.CounterVec
	cliCommands *prometheus.CounterVec
}

// NewMetrics registers the MCP counters on reg.
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
		cliCommands: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_mcp_cli_commands_total",
			Help: "CLI commands run by the MCP lode tool, by command path (e.g. \"task gap\", or unknown) and outcome (the tool call outcomes).",
		}, []string{"command", "outcome"}),
	}
	reg.MustRegister(m.toolCalls, m.oauth, m.cliCommands)
	return m
}

func (m *Metrics) toolCall(tool, outcome string) {
	if m != nil {
		m.toolCalls.WithLabelValues(tool, outcome).Inc()
	}
}

// OAuthRequest counts one OAuth endpoint request (WL-REQ-1825).
func (m *Metrics) OAuthRequest(endpoint, outcome string) {
	if m != nil {
		m.oauth.WithLabelValues(endpoint, outcome).Inc()
	}
}

// cliCommand counts one command the lode tool ran (WL-REQ-1865). command is a
// path in the command tree or "unknown", so the label stays bounded.
func (m *Metrics) cliCommand(command, outcome string) {
	if m != nil {
		m.cliCommands.WithLabelValues(command, outcome).Inc()
	}
}
