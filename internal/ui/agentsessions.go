package ui

import "strings"

// agentLabel turns a model.KnownAgents id into something a person reads.
// Anything unrecognised — including the "other" bucket a client folds an
// unfamiliar harness onto — is shown as-is rather than hidden, so a session
// never renders as a blank row.
func agentLabel(agent string) string {
	switch agent {
	case "claude-code":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "copilot":
		return "Copilot"
	case "cursor":
		return "Cursor"
	case "aider":
		return "Aider"
	case "opencode":
		return "OpenCode"
	case "amp":
		return "Amp"
	case "pi":
		return "Pi"
	case "":
		return "agent"
	default:
		return agent
	}
}

// agentSessionDetail is the secondary line under a session, everything after
// the task link: what the session is on, who is running it, and how long it
// has been alive.
//
// Built as one string rather than composed of templ expressions because templ
// inserts its own whitespace between them — mixing the two produced doubled
// spaces around every separator. One function owning the punctuation is also
// what keeps the two pages' rows reading identically.
//
// A finished session's last-seen is its end, so repeating it would say the
// same thing twice.
func agentSessionDetail(r AgentSessionRow) string {
	parts := make([]string, 0, 4)
	if r.TaskTitle != "" {
		parts = append(parts, r.TaskTitle)
	}
	if r.ActorID != "" {
		parts = append(parts, r.ActorID)
	}
	parts = append(parts, "started "+r.Started)
	if r.Running && r.LastSeen != "" {
		parts = append(parts, "last seen "+r.LastSeen)
	}
	return strings.Join(parts, " \u00b7 ")
}
