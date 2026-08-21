// Package harness holds one adapter per coding-agent harness plus a registry
// (spec 008 §17.1). An adapter is a table of locations and event names; the
// behaviour behind them is always `lode hook <event>`, so no adapter ever
// introduces a second coordination model.
package harness

import "sort"

// Settings scopes, moved from internal/cmd: local settings are the
// developer's own (git-ignored), project settings are committed. Adapters
// whose native config has no such split treat both scopes alike.
const (
	ScopeLocal   = "local"
	ScopeProject = "project"
)

// Event is Worklode's lifecycle vocabulary. PreCommit is deliberately not an
// adapter concern: the git hook covers it for every harness (spec 008 §17.4).
type Event string

const (
	SessionStart  Event = "session-start"
	SessionEnd    Event = "session-end"
	Heartbeat     Event = "heartbeat"
	WorktreeEnter Event = "worktree-enter"
)

// AllEvents is the fixed order reports use.
var AllEvents = []Event{SessionStart, SessionEnd, Heartbeat, WorktreeEnter}

// What an install or uninstall did to one location. A status line is a
// personal choice, so both directions refuse to touch one that is not ours;
// ActionKept is that refusal, reported rather than swallowed.
const (
	ActionInstalled = "installed" // we wrote our entry
	ActionKept      = "kept"      // someone else's entry was left alone
	ActionRemoved   = "removed"   // our entry was removed
	ActionNone      = "none"      // nothing of ours was there
)

// SkillTarget is one directory a harness reads skills from. PerSkill means
// link <Dir>/<name> per skill (the directory is user-owned and must not be
// replaced wholesale); otherwise Dir itself may be created as a symlink to
// the Worklode skills dir.
type SkillTarget struct {
	Dir      string
	PerSkill bool
}

// HookInstall reports what one adapter's InstallHooks wrote. Unbound names
// the Worklode events this harness cannot express — degraded coverage,
// never an install failure (spec 008 §17.1).
type HookInstall struct {
	Path    string
	Bound   []string // harness-native event names actually bound
	Unbound []Event
	// Notes carries adapter-specific advice the install report must show the
	// user — a harness whose hooks stay inert until the user approves them, for
	// instance. Empty for an adapter with nothing to say.
	Notes []string
	// StatusLine is set only by InstallWithStatusLine: the status line shares a
	// config file with the hooks, so the two are written together and reported
	// together. nil means the status line was not part of this call.
	StatusLine *StatusLineAction
}

// HookUninstall mirrors internal/cmd's git-hook action vocabulary.
type HookUninstall struct {
	Path   string
	Action string // ActionRemoved | ActionNone
	// StatusLine mirrors HookInstall's, set only by UninstallWithStatusLine.
	StatusLine *StatusLineAction
}

// StatusLineAction is what one status-line install or uninstall did.
type StatusLineAction struct {
	Path   string
	Action string // ActionInstalled | ActionKept | ActionRemoved | ActionNone
}

// Harness is one coding agent's integration surface (spec 008 §17.1).
type Harness interface {
	ID() string
	// Detect reports whether this harness is configured for repoDir or for
	// the user. `--agent auto` installs whatever fires, so a user-level
	// signal — a bare ~/.claude, say — is enough: auto tracks the developer's
	// tooling, not just this repo's. Name an agent explicitly to install into
	// a repo the user-level check would drag in.
	Detect(repoDir string) (bool, error)
	SkillTargets(repoDir, scope string) ([]SkillTarget, error)
	InstallHooks(repoDir, scope string) (HookInstall, error)
	UninstallHooks(repoDir, scope string) (HookUninstall, error)
	Events() map[Event][]string
}

// StatusLiner is implemented by adapters whose harness has a status-line slot
// that takes a command (spec 008 §17.5). Only claude-code has one in v1, so it
// is an optional interface rather than part of Harness: an adapter without a
// status line should not have to implement two no-op methods.
//
// The status line is not a separate location — it is another key in the same
// config file the hooks go into — so it is installed *with* them rather than
// after them: one read, both mutations, one write. Splitting it in two would
// double the I/O and make the pair non-atomic, leaving hooks written and no
// status line when the second step failed. A caller that does not want the
// status line calls Harness.InstallHooks instead.
type StatusLiner interface {
	InstallWithStatusLine(repoDir, scope string) (HookInstall, error)
	UninstallWithStatusLine(repoDir, scope string) (HookUninstall, error)
}

var registry = map[string]Harness{}

func register(h Harness) { registry[h.ID()] = h }

// Get returns the adapter for id.
func Get(id string) (Harness, bool) { h, ok := registry[id]; return h, ok }

// IDs returns every registered adapter id, sorted.
func IDs() []string {
	out := make([]string, 0, len(registry))
	for id := range registry {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Detected returns the ids (sorted) whose Detect fires for repoDir. A
// Detect error skips that adapter — auto-detection must never fail install.
func Detected(repoDir string) []string {
	var out []string
	for _, id := range IDs() {
		if ok, err := registry[id].Detect(repoDir); err == nil && ok {
			out = append(out, id)
		}
	}
	return out
}

// missingEvents lists AllEvents entries absent from h.Events(), in AllEvents
// order. Every adapter reports degraded coverage the same way.
func missingEvents(h Harness) []Event {
	events := h.Events()
	var out []Event
	for _, e := range AllEvents {
		if len(events[e]) == 0 {
			out = append(out, e)
		}
	}
	return out
}
