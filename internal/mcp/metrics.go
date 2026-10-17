// metrics.go holds the MCP tool server's Prometheus metrics (WL-REQ-32).
package mcp

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// mcpOutcomes are the outcome label's values: ok, or why the call failed.
var mcpOutcomes = []string{"ok", "invalid", "project_required", "confirm_project", "not_found", "refused", "conflict", "error"}

// mcpMetrics counts tool calls. Nil-safe: a server built with no
// Registerer has none and still serves.
type mcpMetrics struct {
	calls *prometheus.CounterVec
}

func newMCPMetrics(reg prometheus.Registerer) (*mcpMetrics, error) {
	if reg == nil {
		return nil, nil
	}
	m := &mcpMetrics{calls: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_mcp_tool_calls_total",
		Help: "MCP tool calls at /mcp, by tool and outcome (" + strings.Join(mcpOutcomes, ", ") +
			"). Labels are bounded: the project, the actor and the arguments are deliberately not among them.",
	}, []string{"tool", "outcome"})}
	for _, t := range mcpToolNames {
		for _, o := range mcpOutcomes {
			m.calls.WithLabelValues(t, o)
		}
	}
	if err := reg.Register(m.calls); err != nil {
		return nil, fmt.Errorf("mcp: register metrics: %w", err)
	}
	return m, nil
}

func (m *mcpMetrics) call(tool, outcome string) {
	if m != nil {
		m.calls.WithLabelValues(tool, outcome).Inc()
	}
}
