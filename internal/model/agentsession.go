package model

import "time"

// AgentSession is the wire form of an agent session on a task's lease.
// LeaseID identifies the lease this session is recorded against, not a
// leaked surrogate key: (lease_id, agent, session_id) is the session's
// natural key, so callers need it to address the session unambiguously.
// Usage is whatever a previous touch or end reported; nil until one does.
// CostAmount is a decimal string so it round-trips through numeric(12,6)
// exactly.
type AgentSession struct {
	LeaseID      int64      `json:"lease_id"`
	Agent        string     `json:"agent"`
	AgentVersion string     `json:"agent_version,omitempty"`
	SessionID    string     `json:"session_id"`
	StartedAt    time.Time  `json:"started_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	InputTokens  *int64     `json:"input_tokens"`
	OutputTokens *int64     `json:"output_tokens"`
	CostAmount   *string    `json:"cost_amount"`
	CostCurrency string     `json:"cost_currency"`
}

// SessionUsageBucket is one day's tokens on one model at one billing speed —
// the granularity a price can be applied at. The classes travel separately
// because they are priced up to 20x apart and a total cannot be repriced
// back into them (see store.TokenCounts).
type SessionUsageBucket struct {
	Day                string `json:"day"` // YYYY-MM-DD, UTC
	Model              string `json:"model"`
	Speed              string `json:"speed"` // "standard" (default) or "fast"
	InputTokens        int64  `json:"input_tokens"`
	CacheWrite5mTokens int64  `json:"cache_write_5m_tokens"`
	CacheWrite1hTokens int64  `json:"cache_write_1h_tokens"`
	CacheReadTokens    int64  `json:"cache_read_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
}
