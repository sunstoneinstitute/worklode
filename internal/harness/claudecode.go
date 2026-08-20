package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// StatusLineCommand is what the status-line install binds, and — by the
// same command-as-marker trick — how uninstall recognizes its own entry.
const StatusLineCommand = "lode statusline"

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
var claudeBindings = []hookBinding{
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

// Events is claudeBindings read the other way round, so the event table
// cannot drift from what install actually writes (spec 008 §17.1).
func (ClaudeCode) Events() map[Event][]string { return eventsFor(claudeBindings) }

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
	return HookInstall{Path: path, Bound: boundNames(claudeBindings)}, nil
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
	return installGroupedHooks(path, claudeBindings)
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
// path, reporting ActionNone or ActionRemoved.
func uninstallClaudeHooks(path string) (action string, err error) {
	return uninstallGroupedHooks(path)
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
