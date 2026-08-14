package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// Settings scopes. Local settings are the developer's own and are normally
// git-ignored; project settings are committed and shared by the whole repo.
const (
	scopeLocal   = "local"
	scopeProject = "project"
)

// lodeHookPrefix marks a settings entry as Worklode's. JSON has no comments,
// so the command itself is the marker: install strips every entry with this
// prefix before writing the current set, which makes a re-run converge rather
// than duplicate.
const lodeHookPrefix = "lode hook "

// lodeStatusLineCommand is what the status-line install binds, and — by the
// same command-as-marker trick — how uninstall recognizes its own entry.
const lodeStatusLineCommand = "lode statusline"

// What an install or uninstall did to the status line. A status line is a
// personal choice, so both directions refuse to touch one that is not ours;
// hookActionKept is that refusal, reported rather than swallowed.
const (
	hookActionInstalled = "installed" // we wrote our status line
	hookActionKept      = "kept"      // someone else's status line was left alone
)

// claudeBinding is one Claude Code hook binding. An empty Matcher means the
// binding applies to every occurrence of the event.
type claudeBinding struct {
	Event   string
	Matcher string
	Command string
}

// claudeBindings is every Claude Code event Worklode listens to. Heartbeat is
// bound to four events because Stop alone leaves a live session looking dead:
// StopFailure replaces Stop when a turn dies on an API error, SubagentStop
// covers a long subagent fan-out, and Notification covers a session blocked on
// a human.
//
// WorktreeCreate and WorktreeRemove are deliberately absent. They are
// delegation hooks, not notifications: binding one makes it *the* worktree
// creator, replacing Claude Code's built-in `git worktree add`, and
// EnterWorktree then fails unless the hook prints the path it created.
// Worklode observes rather than creates, so binding them broke EnterWorktree
// outright. Nothing is lost by dropping them — Claude Code creates worktrees
// under .claude/worktrees/, which the default layout rejects, so both
// handlers were unreachable NOPs on that path. Worklode's own worktrees
// are covered by session-start (which auto-resumes an abandoned lease) and by
// the worktree-enter binding below; `lode hook worktree-create` and
// `worktree-remove` remain available for scripts that do create them.
var claudeBindings = []claudeBinding{
	{Event: "SessionStart", Command: "lode hook session-start"},
	{Event: "SessionEnd", Command: "lode hook session-end"},
	{Event: "Stop", Command: "lode hook heartbeat"},
	{Event: "StopFailure", Command: "lode hook heartbeat"},
	{Event: "SubagentStop", Command: "lode hook heartbeat"},
	{Event: "Notification", Command: "lode hook heartbeat"},
	{Event: "PostToolUse", Matcher: "EnterWorktree", Command: "lode hook worktree-enter"},
}

// settingsPathForScope resolves the settings file for scope, relative to the
// git worktree root containing dir.
func settingsPathForScope(dir, scope string) (string, error) {
	root, ok := worktree.Root(dir)
	if !ok {
		return "", fmt.Errorf("not inside a git repository: %s", dir)
	}
	return claudeSettingsPath(root, scope)
}

// claudeSettingsPath maps a scope to its settings file under root.
func claudeSettingsPath(root, scope string) (string, error) {
	switch scope {
	case scopeLocal:
		return filepath.Join(root, ".claude", "settings.local.json"), nil
	case scopeProject:
		return filepath.Join(root, ".claude", "settings.json"), nil
	default:
		return "", fmt.Errorf("unknown scope %q: want %q or %q", scope, scopeLocal, scopeProject)
	}
}

// readSettingsFile reads path as generic JSON. A missing file is an empty
// settings object, not an error — installing into a repo that has never had
// Claude Code settings is the common case.
func readSettingsFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return settings, nil
}

// writeSettingsFile writes settings back to path, creating the .claude
// directory if needed. Output is indented and newline-terminated so a
// committed settings file stays readable and diffs cleanly.
func writeSettingsFile(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// installClaudeHooks writes Worklode's bindings into the settings file at
// path, replacing any bindings a previous install left behind and preserving
// every other setting.
func installClaudeHooks(path string) error {
	settings, err := readSettingsFile(path)
	if err != nil {
		return err
	}
	hooks, _ := stripLodeHooks(settingsHooks(settings))
	for _, b := range claudeBindings {
		hooks[b.Event] = appendBinding(hooks[b.Event], b)
	}
	settings["hooks"] = hooks
	return writeSettingsFile(path, settings)
}

// uninstallClaudeHooks removes Worklode's bindings from the settings file at
// path, reporting hookActionNone or hookActionRemoved (the same vocabulary
// uninstallGitHooks uses). A missing file, or one with no `lode hook` entries
// to strip, is hookActionNone and leaves the file untouched — a no-op must
// not reformat someone's settings JSON or bump its mtime.
func uninstallClaudeHooks(path string) (action string, err error) {
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return hookActionNone, nil
	}
	settings, err := readSettingsFile(path)
	if err != nil {
		return "", err
	}
	hooks, changed := stripLodeHooks(settingsHooks(settings))
	if !changed {
		return hookActionNone, nil
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	if err := writeSettingsFile(path, settings); err != nil {
		return "", err
	}
	return hookActionRemoved, nil
}

// installStatusLine points the settings file at `lode statusline`, but only
// when no status line is configured. A status line is a personal choice and a
// slot that holds exactly one command, so replacing one the user chose would
// be a silent theft rather than an install; that case reports hookActionKept
// and leaves the file untouched. A re-run over our own entry rewrites it in
// place, so install converges.
func installStatusLine(path string) (action string, err error) {
	settings, err := readSettingsFile(path)
	if err != nil {
		return "", err
	}
	if existing, ok := settings["statusLine"]; ok && !isLodeStatusLine(existing) {
		return hookActionKept, nil
	}
	settings["statusLine"] = map[string]any{
		"type":    "command",
		"command": lodeStatusLineCommand,
	}
	if err := writeSettingsFile(path, settings); err != nil {
		return "", err
	}
	return hookActionInstalled, nil
}

// uninstallStatusLine removes our status line from the settings file at path.
// A missing file, no status line at all, or someone else's is left exactly as
// found — hookActionNone and hookActionKept respectively — because a no-op
// must not reformat someone's settings JSON or bump its mtime.
func uninstallStatusLine(path string) (action string, err error) {
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return hookActionNone, nil
	}
	settings, err := readSettingsFile(path)
	if err != nil {
		return "", err
	}
	existing, ok := settings["statusLine"]
	if !ok {
		return hookActionNone, nil
	}
	if !isLodeStatusLine(existing) {
		return hookActionKept, nil
	}
	delete(settings, "statusLine")
	if err := writeSettingsFile(path, settings); err != nil {
		return "", err
	}
	return hookActionRemoved, nil
}

// isLodeStatusLine reports whether a statusLine setting runs `lode statusline`.
// The command may carry flags or an absolute path to the binary, so the match
// is on the command word rather than on equality.
func isLodeStatusLine(v any) bool {
	entry, ok := v.(map[string]any)
	if !ok {
		return false
	}
	command, ok := entry["command"].(string)
	if !ok {
		return false
	}
	fields := strings.Fields(command)
	for i, f := range fields {
		if filepath.Base(f) == "lode" {
			return i+1 < len(fields) && fields[i+1] == "statusline"
		}
	}
	return false
}

// settingsHooks returns the settings' "hooks" object, or an empty one when it
// is absent or not an object.
func settingsHooks(settings map[string]any) map[string]any {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return hooks
}

// appendBinding adds b to an event's existing group list, which may be nil or
// a non-list left by hand-editing (in which case it is replaced).
func appendBinding(existing any, b claudeBinding) []any {
	groups, _ := existing.([]any)
	group := map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": b.Command}},
	}
	if b.Matcher != "" {
		group["matcher"] = b.Matcher
	}
	return append(groups, group)
}

// stripLodeHooks removes every `lode hook` entry from a hooks object, dropping
// groups and events that end up empty so an uninstall leaves no residue. Any
// third-party hook sharing an event is preserved. changed reports whether any
// entry was actually removed, so a caller can tell a genuine removal from a
// no-op and skip rewriting the file for the latter.
func stripLodeHooks(hooks map[string]any) (out map[string]any, changed bool) {
	out = map[string]any{}
	for event, raw := range hooks {
		groups, ok := raw.([]any)
		if !ok {
			// Not a shape we wrote; leave it exactly as found.
			out[event] = raw
			continue
		}
		var kept []any
		for _, g := range groups {
			group, ok := g.(map[string]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			entries, ok := group["hooks"].([]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			var keptEntries []any
			for _, e := range entries {
				if isLodeHookEntry(e) {
					changed = true
					continue
				}
				keptEntries = append(keptEntries, e)
			}
			if len(keptEntries) == 0 {
				continue
			}
			group["hooks"] = keptEntries
			kept = append(kept, group)
		}
		if len(kept) == 0 {
			continue
		}
		out[event] = kept
	}
	return out, changed
}

// isLodeHookEntry reports whether one hook entry runs a `lode hook` command.
func isLodeHookEntry(e any) bool {
	entry, ok := e.(map[string]any)
	if !ok {
		return false
	}
	command, ok := entry["command"].(string)
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(command), lodeHookPrefix)
}
