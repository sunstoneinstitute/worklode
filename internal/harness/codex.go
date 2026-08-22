package harness

import (
	"os"
	"path/filepath"
)

// codexBindings is every Codex event Worklode listens to. Codex's hooks.json
// uses the same event → matcher-group → handler shape as Claude Code, so the
// shared merge helpers write it. WorktreeEnter has no Codex event behind it
// and is reported unbound.
//
// Each command carries --harness codex so the handler knows which transcript
// shape to expect; the `lode hook ` prefix stays first because that prefix is
// the marker install and uninstall strip on.
var codexBindings = []hookBinding{
	{Event: "SessionStart", Command: "lode hook session-start --harness codex"},
	{Event: "SessionEnd", Command: "lode hook session-end --harness codex"},
	{Event: "Stop", Command: "lode hook heartbeat --harness codex"},
	{Event: "SubagentStop", Command: "lode hook heartbeat --harness codex"},
}

// codexTrustNote is install-time advice, not a warning: Codex records trust
// against a hook definition's hash, so a freshly written hook does nothing
// until the user reviews it and every later edit needs re-approval.
const codexTrustNote = "hooks are written but stay inactive until you review them with /hooks in Codex; " +
	"trust is recorded per hook definition, so re-approve after any change"

// Codex is the codex adapter: hook bindings in $CODEX_HOME/hooks.json
// (default ~/.codex/hooks.json). Both scopes write that user-level file —
// the `lode hook` guard is what scopes behaviour to Worklode worktrees.
//
// Codex also supports a project-level .codex/hooks.json, but it is silently
// ignored while Codex runs inside a git worktree (openai/codex#27133), which
// is exactly where every Worklode task runs. Writing there would produce
// hooks that never fire for Worklode's primary use case, so this adapter
// never does — deliberately, not for lack of a project-scope location.
type Codex struct{}

func init() { register(Codex{}) }

func (Codex) ID() string { return "codex" }

// codexHome resolves Codex's config directory.
func codexHome() (string, error) {
	if v := os.Getenv("CODEX_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

// codexHooksPath is the one file this adapter writes, for either scope.
func codexHooksPath() (string, error) {
	dir, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hooks.json"), nil
}

// Detect: Codex is configured for the user (its config directory exists).
func (Codex) Detect(repoDir string) (bool, error) {
	dir, err := codexHome()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(dir)
	return err == nil, nil
}

// SkillTargets: ~/.agents/skills, the cross-harness personal skills directory
// Codex reads (spec 008 §17.3). The directory itself may be the symlink, so
// this is not a per-skill target.
func (Codex) SkillTargets(repoDir, scope string) ([]SkillTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return []SkillTarget{{Dir: filepath.Join(home, ".agents", "skills")}}, nil
}

// Events is codexBindings read the other way round, so the event table cannot
// drift from what install actually writes (spec 008 §17.1).
func (Codex) Events() map[Event][]string { return eventsFor(codexBindings) }

// InstallHooks merges Worklode's bindings into hooks.json, preserving every
// foreign hook and top-level key it finds.
func (c Codex) InstallHooks(repoDir, scope string) (HookInstall, error) {
	path, err := codexHooksPath()
	if err != nil {
		return HookInstall{}, err
	}
	if err := installGroupedHooks(path, codexBindings); err != nil {
		return HookInstall{}, err
	}
	return HookInstall{
		Path:    path,
		Bound:   boundNames(codexBindings),
		Unbound: missingEvents(c),
		Notes:   []string{codexTrustNote},
	}, nil
}

// UninstallHooks strips Worklode's bindings from hooks.json, leaving the file
// untouched when there is nothing of ours in it.
func (Codex) UninstallHooks(repoDir, scope string) (HookUninstall, error) {
	path, err := codexHooksPath()
	if err != nil {
		return HookUninstall{}, err
	}
	action, err := uninstallGroupedHooks(path)
	if err != nil {
		return HookUninstall{}, err
	}
	return HookUninstall{Path: path, Action: action}, nil
}
