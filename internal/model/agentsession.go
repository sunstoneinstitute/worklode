package model

import "time"

// AgentOther is the agent id for a harness worklode has no dedicated id for.
const AgentOther = "other"

// KnownAgents is the vocabulary of AgentSession.Agent, mirroring the
// agent_sessions.agent CHECK constraint (migration 0033). It lives here
// rather than in internal/store because both ends need it: the store rejects
// anything outside it, and a client that reports an agent has to know what
// the server will accept before it sends one.
var KnownAgents = []string{
	"claude-code", "codex", "copilot", "cursor", "aider",
	"opencode", "pi", "amp", AgentOther,
}

// knownAgents indexes KnownAgents for lookup.
var knownAgents = func() map[string]bool {
	m := make(map[string]bool, len(KnownAgents))
	for _, a := range KnownAgents {
		m[a] = true
	}
	return m
}()

// AgentKnown reports whether agent is in KnownAgents.
func AgentKnown(agent string) bool { return knownAgents[agent] }

// NormalizeAgent maps agent into KnownAgents, folding anything unrecognised
// onto AgentOther. Clients reporting a hand-configured agent id should pass
// it through here first: recording an unfamiliar harness as "other" keeps the
// session, where sending it verbatim gets the whole report rejected and the
// session is lost. An empty agent stays empty — a caller with nothing to
// report has a default to apply, not an unknown id to record.
func NormalizeAgent(agent string) string {
	if agent == "" || knownAgents[agent] {
		return agent
	}
	return AgentOther
}

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

// AgentSessionInput is the request body for TouchAgentSession (POST
// /api/v1/tasks/{id}/agent-session): record that an agent session is
// working the task, or heartbeat an existing one.
type AgentSessionInput struct {
	Agent        string `json:"agent"`
	AgentVersion string `json:"agent_version"`
	SessionID    string `json:"session_id"`
	// Usage is the session's spend so far. omitempty: unlike
	// EndAgentSessionInput.Usage, a touch has no "clear it" caller today, so
	// nil and an empty slice are sent identically (the key absent) — the
	// only production caller (internal/hookrun) already collapses "nothing
	// read" to nil for exactly that reason. A caller that later needs to
	// distinguish nil from empty here should drop omitempty and match
	// EndAgentSessionInput's contract instead.
	Usage []SessionUsageBucket `json:"usage,omitempty"`
}

// EndAgentSessionInput is the request body for EndAgentSession (POST
// /api/v1/tasks/{id}/agent-session/end). Only Agent and SessionID are
// required; the rest are optional accounting fields.
type EndAgentSessionInput struct {
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
	Usage []SessionUsageBucket `json:"usage"`
}
