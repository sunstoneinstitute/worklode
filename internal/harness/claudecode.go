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
const StatusLineCommand = "lode-statusline"

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
// below; the compatibility `lode hook worktree-create`/`worktree-remove`
// commands stay callable from
// scripts.
var claudeBindings = []hookBinding{
	{Event: "SessionStart", Command: "lode-hook session-start"},
	{Event: "SessionEnd", Command: "lode-hook session-end"},
	{Event: "Stop", Command: "lode-hook heartbeat"},
	{Event: "StopFailure", Command: "lode-hook heartbeat"},
	{Event: "SubagentStop", Command: "lode-hook heartbeat"},
	{Event: "Notification", Command: "lode-hook heartbeat"},
	{Event: "PostToolUse", Matcher: "EnterWorktree", Command: "lode-hook worktree-enter"},
}

// ClaudeCode is the claude-code adapter: JSON hook bindings in
// .claude/settings*.json. Its command strings use `lode-hook <event>` with
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

// InstallWithStatusLine is InstallHooks plus the status line. Both live in the
// one settings file, so both mutations are applied to a single read and
// committed by a single write: the pair either lands or does not, and a
// declined status line (ActionKept) still costs nothing extra.
func (ClaudeCode) InstallWithStatusLine(repoDir, scope string) (HookInstall, error) {
	path, err := settingsPathForScope(repoDir, scope)
	if err != nil {
		return HookInstall{}, err
	}
	settings, err := ReadJSONFile(path)
	if err != nil {
		return HookInstall{}, err
	}
	applyGroupedHooks(settings, claudeBindings)
	action := applyStatusLine(settings)
	if err := writeJSONFile(path, settings); err != nil {
		return HookInstall{}, err
	}
	return HookInstall{
		Path:       path,
		Bound:      boundNames(claudeBindings),
		StatusLine: &StatusLineAction{Path: path, Action: action},
	}, nil
}

// UninstallWithStatusLine is UninstallHooks plus the status line, over the same
// single read-modify-write. The file is written only when one of the two
// actually removed something, so an uninstall that finds nothing of ours — or
// only a status line someone else configured — leaves the file byte-identical.
func (ClaudeCode) UninstallWithStatusLine(repoDir, scope string) (HookUninstall, error) {
	path, err := settingsPathForScope(repoDir, scope)
	if err != nil {
		return HookUninstall{}, err
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return HookUninstall{
			Path: path, Action: ActionNone,
			StatusLine: &StatusLineAction{Path: path, Action: ActionNone},
		}, nil
	}
	settings, err := ReadJSONFile(path)
	if err != nil {
		return HookUninstall{}, err
	}
	hooks := stripGroupedHooks(settings)
	statusLine := stripStatusLine(settings)
	if hooks == ActionRemoved || statusLine == ActionRemoved {
		if err := writeJSONFile(path, settings); err != nil {
			return HookUninstall{}, err
		}
	}
	return HookUninstall{
		Path: path, Action: hooks,
		StatusLine: &StatusLineAction{Path: path, Action: statusLine},
	}, nil
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

// PropagateToWorktree gives a freshly created worktree at dir the
// local-scope Claude Code settings its root already has. Local scope is a
// developer's own opt-in file (settings.local.json), and git does not track
// it, so a linked worktree's own checkout never receives it the way it
// inherits committed settings — it starts empty, and an agent working there
// gets none of the repo's hooks, permissions or enabled plugins.
//
// Root's other local settings are carried over key by key, and only for keys
// the worktree does not already set: a worktree that has since diverged keeps
// its own choices, so this converges rather than stomping on re-run. Worklode's
// own bindings are then applied on top, which is what makes a re-run repair a
// worktree whose hooks were removed. The status line is not carried over —
// see the note at the copy.
//
// This only ever mirrors a choice the developer already made at root: a repo
// where `lode install` was never run locally is left alone, so `lode worktree next`
// never opts a worktree into Claude Code hooks on its own.
func (ClaudeCode) PropagateToWorktree(root, dir string) error {
	rootPath, err := claudeSettingsPath(root, ScopeLocal)
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
	dirPath, err := claudeSettingsPath(dir, ScopeLocal)
	if err != nil {
		return err
	}
	settings, err := ReadJSONFile(dirPath)
	if err != nil {
		return err
	}
	// Everything else root sets locally — permissions, enabled plugins, env,
	// the developer's own hooks — is what an agent in the worktree would
	// otherwise be missing. Copied only where the worktree is silent, so its
	// own edits survive.
	//
	// statusLine is the one key held back: it is a slot that holds exactly
	// one command, and applyStatusLine below refuses to take one the user
	// chose. Copying a foreign status line here would install through the
	// back door what that rule exists to protect.
	for k, v := range rootSettings {
		if k == "statusLine" {
			continue
		}
		if _, ok := settings[k]; !ok {
			settings[k] = v
		}
	}
	applyGroupedHooks(settings, claudeBindings)
	if sl, ok := rootSettings["statusLine"]; ok && isLodeStatusLine(sl) {
		applyStatusLine(settings)
	}
	return writeJSONFile(dirPath, settings)
}

// uninstallClaudeHooks removes Worklode's bindings from the settings file at
// path, reporting ActionNone or ActionRemoved.
func uninstallClaudeHooks(path string) (action string, err error) {
	return uninstallGroupedHooks(path)
}

// applyStatusLine points an already-read settings object at `lode-statusline`,
// but only when no status line is configured. A status line is a personal
// choice and a slot that holds exactly one command, so replacing one the user
// chose would be a silent theft rather than an install; that case reports
// ActionKept and leaves settings untouched. A re-run over our own entry
// rewrites it in place, so install converges.
func applyStatusLine(settings map[string]any) (action string) {
	if existing, ok := settings["statusLine"]; ok && !isLodeStatusLine(existing) {
		return ActionKept
	}
	settings["statusLine"] = map[string]any{
		"type":    "command",
		"command": StatusLineCommand,
	}
	return ActionInstalled
}

// stripStatusLine removes our status line from an already-read settings
// object. No status line at all, or someone else's, leaves settings exactly as
// read — ActionNone and ActionKept respectively — so the caller knows not to
// write.
func stripStatusLine(settings map[string]any) (action string) {
	existing, ok := settings["statusLine"]
	if !ok {
		return ActionNone
	}
	if !isLodeStatusLine(existing) {
		return ActionKept
	}
	delete(settings, "statusLine")
	return ActionRemoved
}

// isLodeStatusLine reports whether a statusLine setting runs the current
// `lode-statusline` binary or the legacy `lode statusline` command. The latter
// may carry flags or an absolute lode path, so upgrades can still remove it.
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
	if len(fields) > 0 && filepath.Base(fields[0]) == "lode-statusline" {
		return true
	}
	return len(fields) > 1 && filepath.Base(fields[0]) == "lode" && fields[1] == "statusline"
}
