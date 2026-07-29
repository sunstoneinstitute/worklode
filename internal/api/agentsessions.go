package api

import (
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// agentSessionJSON is the wire form of an agent session.
type agentSessionJSON struct {
	// LeaseID identifies the lease this session is recorded against, not a
	// leaked surrogate key: (lease_id, agent, session_id) is the session's
	// natural key, so callers need it to address the session unambiguously.
	LeaseID      int64      `json:"lease_id"`
	Agent        string     `json:"agent"`
	AgentVersion string     `json:"agent_version,omitempty"`
	SessionID    string     `json:"session_id"`
	StartedAt    time.Time  `json:"started_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	// Usage is whatever a previous end reported; null until one does. Cost
	// is a decimal string for the same reason the end request takes one.
	InputTokens  *int64  `json:"input_tokens"`
	OutputTokens *int64  `json:"output_tokens"`
	CostAmount   *string `json:"cost_amount"`
	CostCurrency string  `json:"cost_currency"`
}

func toAgentSessionJSON(a *store.AgentSession) agentSessionJSON {
	return agentSessionJSON{
		LeaseID:      a.LeaseID,
		Agent:        a.Agent,
		AgentVersion: a.AgentVersion,
		SessionID:    a.SessionID,
		StartedAt:    a.StartedAt,
		LastSeenAt:   a.LastSeenAt,
		EndedAt:      a.EndedAt,
		InputTokens:  a.InputTokens,
		OutputTokens: a.OutputTokens,
		CostAmount:   a.CostAmount,
		CostCurrency: a.CostCurrency,
	}
}

type agentSessionRequest struct {
	Agent        string `json:"agent"`
	AgentVersion string `json:"agent_version"`
	SessionID    string `json:"session_id"`
}

// touchAgentSession handles POST /api/v1/tasks/{id}/agent-session: record
// that an agent session is working the task, or heartbeat an existing one.
// Only the lease holder may report; a non-holder gets 404, the same
// probe-resistant answer as renew.
func (s *server) touchAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req agentSessionRequest
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actor := actorFrom(r)

	sess, err := s.st.TouchAgentSession(r.Context(), id, actor.ID,
		req.Agent, req.AgentVersion, req.SessionID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAgentSessionJSON(sess))
}

type agentSessionEndRequest struct {
	Agent        string `json:"agent"`
	SessionID    string `json:"session_id"`
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	// CostAmount is a decimal string, not a JSON number, so it round-trips
	// through numeric(12,6) exactly (see store.SessionUsage).
	CostAmount   *string `json:"cost_amount"`
	CostCurrency string  `json:"cost_currency"`
}

// endAgentSession handles POST /api/v1/tasks/{id}/agent-session/end: close
// the caller's open session and record whatever usage it reports. Same
// holder policy as touchAgentSession (404 for a non-holder or an unknown
// task); all usage fields are optional, and an already-closed or unknown
// session is also a 404.
func (s *server) endAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req agentSessionEndRequest
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actor := actorFrom(r)

	err := s.st.EndAgentSession(r.Context(), id, actor.ID, req.Agent, req.SessionID,
		store.SessionUsage{
			InputTokens:  req.InputTokens,
			OutputTokens: req.OutputTokens,
			CostAmount:   req.CostAmount,
			CostCurrency: req.CostCurrency,
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
