package harness

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// copilotBindings is every Copilot event Worklode listens to. Copilot's file
// is flat — event → handler list, with no matcher-group layer — so these
// bindings carry no matcher. WorktreeEnter has no Copilot event behind it and
// is reported unbound.
var copilotBindings = []hookBinding{
	{Event: "sessionStart", Command: "lode hook session-start --harness copilot"},
	{Event: "sessionEnd", Command: "lode hook session-end --harness copilot"},
	{Event: "agentStop", Command: "lode hook heartbeat --harness copilot"},
	{Event: "subagentStop", Command: "lode hook heartbeat --harness copilot"},
}

// Copilot is the copilot adapter. Copilot reads every *.json in its hooks
// directory and the filename is arbitrary, so Worklode owns `worklode.json`
// outright: install rewrites it wholesale and uninstall deletes it, and no
// foreign file is ever read or touched.
type Copilot struct{}

func init() { register(Copilot{}) }

func (Copilot) ID() string { return "copilot" }

// copilotHome resolves Copilot's personal config directory.
func copilotHome() (string, error) {
	if v := os.Getenv("COPILOT_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".copilot"), nil
}

// copilotHooksPath maps a scope to the file this adapter owns: the personal
// hooks directory for local scope, the repo's committed one for project.
// Project scope resolves the git root rather than trusting repoDir, which is
// the process working directory: Copilot reads .github/hooks only at the top
// of the repo, so a run from a subdirectory must not write one below it.
func copilotHooksPath(repoDir, scope string) (string, error) {
	switch scope {
	case ScopeLocal:
		home, err := copilotHome()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "hooks", "worklode.json"), nil
	case ScopeProject:
		root, ok := worktree.Root(repoDir)
		if !ok {
			return "", fmt.Errorf("not inside a git repository: %s", repoDir)
		}
		return filepath.Join(root, ".github", "hooks", "worklode.json"), nil
	default:
		return "", fmt.Errorf("unknown scope %q: want %q or %q", scope, ScopeLocal, ScopeProject)
	}
}

// Detect: Copilot is configured for the user, or the repo carries Copilot's
// own instruction file.
func (Copilot) Detect(repoDir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repoDir, ".github", "copilot-instructions.md")); err == nil {
		return true, nil
	}
	dir, err := copilotHome()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(dir)
	return err == nil, nil
}

// SkillTargets: ~/.agents/skills, the cross-harness personal skills directory
// Copilot reads (spec 008 §17.3).
func (Copilot) SkillTargets(repoDir, scope string) ([]SkillTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return []SkillTarget{{Dir: filepath.Join(home, ".agents", "skills")}}, nil
}

// Events is copilotBindings read the other way round, so the event table
// cannot drift from what install actually writes (spec 008 §17.1).
func (Copilot) Events() map[Event][]string { return eventsFor(copilotBindings) }

// InstallHooks writes the whole file. "version": 1 is required — Copilot
// silently ignores a hooks file without it — and each handler binds through
// the cross-platform `command` key rather than `bash`, which would leave
// Windows unbound.
func (c Copilot) InstallHooks(repoDir, scope string) (HookInstall, error) {
	path, err := copilotHooksPath(repoDir, scope)
	if err != nil {
		return HookInstall{}, err
	}
	hooks := map[string]any{}
	for _, b := range copilotBindings {
		hooks[b.Event] = []any{map[string]any{"type": "command", "command": b.Command}}
	}
	if err := writeJSONFile(path, map[string]any{"version": 1, "hooks": hooks}); err != nil {
		return HookInstall{}, err
	}
	return HookInstall{
		Path:    path,
		Bound:   boundNames(copilotBindings),
		Unbound: missingEvents(c),
	}, nil
}

// UninstallHooks deletes the file, which is ours entire. A file that is not
// there is ActionNone, not an error.
func (Copilot) UninstallHooks(repoDir, scope string) (HookUninstall, error) {
	path, err := copilotHooksPath(repoDir, scope)
	if err != nil {
		return HookUninstall{}, err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return HookUninstall{Path: path, Action: ActionNone}, nil
		}
		return HookUninstall{}, fmt.Errorf("remove %s: %w", path, err)
	}
	return HookUninstall{Path: path, Action: ActionRemoved}, nil
}
