package api

import (
	"fmt"
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
	// Usage is whatever a previous touch or end reported; null until one
	// does. Cost is a decimal string for the same reason the requests take
	// one.
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
	// Usage is the session's spend so far, in the same form and with the same
	// nil-vs-empty meaning as the end request's. A session that never ends
	// cleanly reports here or nowhere.
	Usage []usageBucketJSON `json:"usage"`
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
	buckets, err := toUsageBuckets(req.Usage)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	actor := actorFrom(r)

	sess, err := s.st.TouchAgentSession(r.Context(), id, actor.ID,
		req.Agent, req.AgentVersion, req.SessionID, buckets)
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
	// Usage is the per-day, per-model breakdown the server prices; when
	// present it supersedes the scalars above. No omitempty on the way in:
	// absent means "leave the recorded usage alone" and [] means "clear it",
	// and only a nil-vs-empty slice keeps those apart.
	Usage []usageBucketJSON `json:"usage"`
}

// usageBucketJSON is one day's tokens on one model at one billing speed. The
// classes travel separately because they are priced up to 20x apart and a
// total cannot be repriced back into them (see store.TokenCounts).
type usageBucketJSON struct {
	Day                string `json:"day"` // YYYY-MM-DD, UTC
	Model              string `json:"model"`
	Speed              string `json:"speed"` // "standard" (default) or "fast"
	InputTokens        int64  `json:"input_tokens"`
	CacheWrite5mTokens int64  `json:"cache_write_5m_tokens"`
	CacheWrite1hTokens int64  `json:"cache_write_1h_tokens"`
	CacheReadTokens    int64  `json:"cache_read_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
}

// toUsageBuckets converts reported buckets to their store form, preserving
// nil (leave usage untouched) against empty (clear it).
//
// The day and the model are checked here so a client that garbles either gets
// a 400 naming the field, rather than the 422 the store's own validation
// would report from inside the write.
func toUsageBuckets(in []usageBucketJSON) ([]store.SessionUsageBucket, error) {
	if in == nil {
		return nil, nil
	}
	out := make([]store.SessionUsageBucket, 0, len(in))
	for _, b := range in {
		day, err := time.Parse(time.DateOnly, b.Day)
		if err != nil {
			return nil, fmt.Errorf("usage day %q: want YYYY-MM-DD", b.Day)
		}
		if b.Model == "" {
			return nil, fmt.Errorf("usage bucket for %s is missing model", b.Day)
		}
		out = append(out, store.SessionUsageBucket{
			Day:   day,
			Model: b.Model,
			Speed: b.Speed,
			Tokens: store.TokenCounts{
				Input:        b.InputTokens,
				CacheWrite5m: b.CacheWrite5mTokens,
				CacheWrite1h: b.CacheWrite1hTokens,
				CacheRead:    b.CacheReadTokens,
				Output:       b.OutputTokens,
			},
		})
	}
	return out, nil
}

// endAgentSession handles POST /api/v1/tasks/{id}/agent-session/end: close
// the caller's open session and record whatever usage it reports. Same
// holder policy as touchAgentSession (404 for a non-holder or an unknown
// task); all usage fields are optional, and an already-closed or unknown
// session is also a 404. A reported usage breakdown is priced by the store,
// which also rebuilds the project's daily rollup from it.
func (s *server) endAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req agentSessionEndRequest
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	buckets, err := toUsageBuckets(req.Usage)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	actor := actorFrom(r)

	err = s.st.EndAgentSession(r.Context(), id, actor.ID, req.Agent, req.SessionID,
		store.SessionUsage{
			InputTokens:  req.InputTokens,
			OutputTokens: req.OutputTokens,
			CostAmount:   req.CostAmount,
			CostCurrency: req.CostCurrency,
			Buckets:      buckets,
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
