package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// lodeHookPrefix marks a settings entry as Worklode's. JSON has no comments,
// so the command itself is the marker: install strips every entry with this
// prefix before writing the current set, which makes a re-run converge rather
// than duplicate.
const lodeHookPrefix = "lode hook "

// StatusLineCommand is what the status-line install binds, and — by the
// same command-as-marker trick — how uninstall recognizes its own entry.
const StatusLineCommand = "lode statusline"

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
// WorktreeCreate and WorktreeRemove are deliberately absent: they are
// delegation hooks, so binding one makes Worklode *the* worktree creator in
// place of Claude Code's own. Worklode observes rather than creates. Its
// worktrees are covered by session-start and the worktree-enter binding
// below; `lode hook worktree-create`/`worktree-remove` stay callable from
// scripts.
var claudeBindings = []claudeBinding{
	{Event: "SessionStart", Command: "lode hook session-start"},
	{Event: "SessionEnd", Command: "lode hook session-end"},
	{Event: "Stop", Command: "lode hook heartbeat"},
	{Event: "StopFailure", Command: "lode hook heartbeat"},
	{Event: "SubagentStop", Command: "lode hook heartbeat"},
	{Event: "Notification", Command: "lode hook heartbeat"},
	{Event: "PostToolUse", Matcher: "EnterWorktree", Command: "lode hook worktree-enter"},
}

// ClaudeCode is the claude-code adapter: JSON hook bindings in
// .claude/settings*.json. Its command strings stay `lode hook <event>` with
// no --harness flag: claude-code is the default harness, and the bare
// prefix is what makes uninstall recognize bindings from installs that
// predate this package.
type ClaudeCode struct{}

var _ StatusLiner = ClaudeCode{}

func init() { register(ClaudeCode{}) }

func (ClaudeCode) ID() string { return "claude-code" }

// Detect: a .claude directory in the repo, or Claude Code configured for
// the user (~/.claude exists).
func (ClaudeCode) Detect(repoDir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repoDir, ".claude")); err == nil {
		return true, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(home, ".claude"))
	return err == nil, nil
}

// SkillTargets: ~/.claude/skills, per-skill — the directory is user-owned
// (spec 008 §17.3). Claude Code reads no project-scope shared dir.
func (ClaudeCode) SkillTargets(repoDir, scope string) ([]SkillTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return []SkillTarget{{Dir: filepath.Join(home, ".claude", "skills"), PerSkill: true}}, nil
}

// Events is claudeBindings read the other way round: the command a binding
// runs names the Worklode event, so the event table cannot restate — and so
// cannot drift from — what install actually writes (spec 008 §17.1).
func (ClaudeCode) Events() map[Event][]string {
	out := map[Event][]string{}
	for _, b := range claudeBindings {
		event := Event(strings.TrimPrefix(b.Command, lodeHookPrefix))
		out[event] = append(out[event], nativeName(b))
	}
	return out
}

// InstallHooks writes Worklode's bindings into the scope's settings file for
// the repo containing repoDir. Every Worklode event claude-code can express
// is bound, so nothing is reported unbound.
func (ClaudeCode) InstallHooks(repoDir, scope string) (HookInstall, error) {
	path, err := settingsPathForScope(repoDir, scope)
	if err != nil {
		return HookInstall{}, err
	}
	if err := installClaudeHooks(path); err != nil {
		return HookInstall{}, err
	}
	return HookInstall{Path: path, Bound: boundNames()}, nil
}

// UninstallHooks removes Worklode's bindings from the scope's settings file
// for the repo containing repoDir.
func (ClaudeCode) UninstallHooks(repoDir, scope string) (HookUninstall, error) {
	path, err := settingsPathForScope(repoDir, scope)
	if err != nil {
		return HookUninstall{}, err
	}
	action, err := uninstallClaudeHooks(path)
	if err != nil {
		return HookUninstall{}, err
	}
	return HookUninstall{Path: path, Action: action}, nil
}

// InstallStatusLine points the scope's settings file at `lode statusline`.
func (ClaudeCode) InstallStatusLine(repoDir, scope string) (StatusLineAction, error) {
	path, err := settingsPathForScope(repoDir, scope)
	if err != nil {
		return StatusLineAction{}, err
	}
	action, err := installStatusLine(path)
	if err != nil {
		return StatusLineAction{}, err
	}
	return StatusLineAction{Path: path, Action: action}, nil
}

// UninstallStatusLine removes our status line from the scope's settings file.
func (ClaudeCode) UninstallStatusLine(repoDir, scope string) (StatusLineAction, error) {
	path, err := settingsPathForScope(repoDir, scope)
	if err != nil {
		return StatusLineAction{}, err
	}
	action, err := uninstallStatusLine(path)
	if err != nil {
		return StatusLineAction{}, err
	}
	return StatusLineAction{Path: path, Action: action}, nil
}

// boundNames lists the harness-native event names an install writes.
func boundNames() []string {
	out := make([]string, 0, len(claudeBindings))
	for _, b := range claudeBindings {
		out = append(out, nativeName(b))
	}
	return out
}

// nativeName is how one binding is spelled outside the adapter: the Claude
// Code event, qualified by its matcher when it has one.
func nativeName(b claudeBinding) string {
	if b.Matcher == "" {
		return b.Event
	}
	return b.Event + ":" + b.Matcher
}

// settingsPathForScope resolves the settings file for scope, relative to the
// git worktree root containing dir.
func settingsPathForScope(dir, scope string) (string, error) {
	root, ok := worktree.Root(dir)
	if !ok {
		return "", fmt.Errorf("not inside a git repository: %s", dir)
	}
	return ClaudeSettingsPath(root, scope)
}

// ClaudeSettingsPath maps a scope to its settings file under root.
func ClaudeSettingsPath(root, scope string) (string, error) {
	switch scope {
	case ScopeLocal:
		return filepath.Join(root, ".claude", "settings.local.json"), nil
	case ScopeProject:
		return filepath.Join(root, ".claude", "settings.json"), nil
	default:
		return "", fmt.Errorf("unknown scope %q: want %q or %q", scope, ScopeLocal, ScopeProject)
	}
}

// installClaudeHooks writes Worklode's bindings into the settings file at
// path, replacing any bindings a previous install left behind and preserving
// every other setting.
func installClaudeHooks(path string) error {
	settings, err := ReadJSONFile(path)
	if err != nil {
		return err
	}
	hooks, _ := stripLodeHooks(settingsHooks(settings))
	for _, b := range claudeBindings {
		hooks[b.Event] = appendBinding(hooks[b.Event], b)
	}
	settings["hooks"] = hooks
	return WriteJSONFile(path, settings)
}

// PropagateToWorktree mirrors root's local-scope Claude Code
// bindings — and, if it is ours, the status line — into a freshly created
// worktree at dir. Local scope is a developer's own opt-in file
// (settings.local.json), and git does not track it, so a linked worktree's
// own checkout never receives it the way it inherits committed settings.
// This only ever mirrors a choice the developer already made at root: a repo
// where `lode install` was never run locally is left alone, so `lode next`
// never opts a worktree into Claude Code hooks on its own.
func (ClaudeCode) PropagateToWorktree(root, dir string) error {
	rootPath, err := ClaudeSettingsPath(root, ScopeLocal)
	if err != nil {
		return err
	}
	rootSettings, err := ReadJSONFile(rootPath)
	if err != nil {
		return err
	}
	if _, installed := stripLodeHooks(settingsHooks(rootSettings)); !installed {
		return nil
	}
	dirPath, err := ClaudeSettingsPath(dir, ScopeLocal)
	if err != nil {
		return err
	}
	if err := installClaudeHooks(dirPath); err != nil {
		return err
	}
	if sl, ok := rootSettings["statusLine"]; ok && isLodeStatusLine(sl) {
		if _, err := installStatusLine(dirPath); err != nil {
			return err
		}
	}
	return nil
}

// uninstallClaudeHooks removes Worklode's bindings from the settings file at
// path, reporting ActionNone or ActionRemoved (the same vocabulary the git
// hooks use). A missing file, or one with no `lode hook` entries to strip, is
// ActionNone and leaves the file untouched — a no-op must not reformat
// someone's settings JSON or bump its mtime.
func uninstallClaudeHooks(path string) (action string, err error) {
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return ActionNone, nil
	}
	settings, err := ReadJSONFile(path)
	if err != nil {
		return "", err
	}
	hooks, changed := stripLodeHooks(settingsHooks(settings))
	if !changed {
		return ActionNone, nil
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	if err := WriteJSONFile(path, settings); err != nil {
		return "", err
	}
	return ActionRemoved, nil
}

// installStatusLine points the settings file at `lode statusline`, but only
// when no status line is configured. A status line is a personal choice and a
// slot that holds exactly one command, so replacing one the user chose would
// be a silent theft rather than an install; that case reports ActionKept
// and leaves the file untouched. A re-run over our own entry rewrites it in
// place, so install converges.
func installStatusLine(path string) (action string, err error) {
	settings, err := ReadJSONFile(path)
	if err != nil {
		return "", err
	}
	if existing, ok := settings["statusLine"]; ok && !isLodeStatusLine(existing) {
		return ActionKept, nil
	}
	settings["statusLine"] = map[string]any{
		"type":    "command",
		"command": StatusLineCommand,
	}
	if err := WriteJSONFile(path, settings); err != nil {
		return "", err
	}
	return ActionInstalled, nil
}

// uninstallStatusLine removes our status line from the settings file at path.
// A missing file, no status line at all, or someone else's is left exactly as
// found — ActionNone and ActionKept respectively — because a no-op
// must not reformat someone's settings JSON or bump its mtime.
func uninstallStatusLine(path string) (action string, err error) {
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return ActionNone, nil
	}
	settings, err := ReadJSONFile(path)
	if err != nil {
		return "", err
	}
	existing, ok := settings["statusLine"]
	if !ok {
		return ActionNone, nil
	}
	if !isLodeStatusLine(existing) {
		return ActionKept, nil
	}
	delete(settings, "statusLine")
	if err := WriteJSONFile(path, settings); err != nil {
		return "", err
	}
	return ActionRemoved, nil
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
