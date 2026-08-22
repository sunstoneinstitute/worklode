package harness

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"text/template"
)

// ampBindings is every Amp event Worklode listens to, in the vocabulary of
// Amp's Plugin API rather than its settings hooks. Amp's JSON hook actions can
// only send a user message or redact tool input, so the Plugin API — a
// TypeScript file Amp runs under Bun, with a shell in hand — is the only Amp
// surface that reaches `lode hook`.
//
// Heartbeat is bound to both ends of a turn. Amp has no Stop event and no
// idle or notification event, so a turn's start and end are the only signals
// there are: a session heartbeats when the user submits a prompt and again
// when the agent finishes, and nothing in between. That is thinner than
// claude-code's four heartbeat sources, and thin enough to matter only for a
// turn longer than the lease TTL.
//
// tool.call and tool.result are deliberately unbound even though the API
// offers them and would close that gap. Both are request events on the
// agent's critical path — a handler runs before the tool executes, and again
// before its result reaches the model — so binding them would put a `lode
// hook` subprocess between the agent and every tool call it makes, dozens of
// times a turn. Claude Code declines the same trade with PostToolUse, though
// it can afford to: it has Notification and SubagentStop to fall back on and
// Amp has neither.
//
// SessionEnd and WorktreeEnter have nothing behind them, for two different
// reasons. Amp's Plugin API stops at session.start, agent.start/end and
// tool.call/result — there is no session-end event at all. WorktreeEnter is
// bound elsewhere to the EnterWorktree tool, which is Claude Code's, not
// Amp's. Both are reported unbound.
var ampBindings = []hookBinding{
	{Event: "session.start", Command: "lode hook session-start --harness amp"},
	{Event: "agent.start", Command: "lode hook heartbeat --harness amp"},
	{Event: "agent.end", Command: "lode hook heartbeat --harness amp"},
}

// ampCeilingNote says why two events stay unbound. The install report already
// names them; this says the ceiling is Amp's API rather than an install that
// fell short, so nobody goes looking for the setting that would fix it.
const ampCeilingNote = "Amp's Plugin API has no session-end event, and worktree-enter tracks a Claude Code " +
	"tool Amp does not have, so both stay unbound whatever is installed; the git pre-commit " +
	"heartbeat still covers commit cadence"

// ampReloadNote is install-time advice: Amp loads its plugins at startup, so a
// freshly written plugin does nothing in the session that installed it.
const ampReloadNote = "Amp loads plugins at startup, so restart Amp to pick this one up"

// ampPluginMarker identifies the generated plugin as Worklode's own — the same
// word githooks.Marker uses, spelled for a TypeScript comment. Uninstall
// removes only a file carrying it, so a `worklode.ts` someone else wrote is
// never deleted.
const ampPluginMarker = "worklode-hook"

//go:embed ampplugin.ts.tmpl
var ampPluginTemplate string

// ampPlugin renders the plugin from ampBindings, so the file Amp executes and
// the event table Events() reports are the same table read twice — an adapter
// cannot claim a binding its generated code does not make.
var ampPlugin = template.Must(template.New("ampplugin.ts").Funcs(template.FuncMap{
	"js": func(s string) (string, error) {
		b, err := json.Marshal(s)
		return string(b), err
	},
}).Parse(ampPluginTemplate))

// ampCommandPattern is what a binding's command may look like before it is
// pasted, unquoted, into the generated file's shell template. The table is
// ours, so this guards against a future edit rather than against input:
// anything outside this shape would be shell text in a place that cannot quote
// it. ampPluginSource enforces it on the way out, so no install can write a
// command that has not passed it.
var ampCommandPattern = regexp.MustCompile(`^lode hook [a-z][a-z-]* --harness amp$`)

// Amp is the amp adapter, and the only code-generating one: where every other
// harness reads a config file Worklode merges into, Amp reads a TypeScript
// plugin Worklode writes whole. That makes the file ours outright — install
// rewrites it, uninstall deletes it — and leaves Amp's settings.json, which
// this adapter still locates for Detect, untouched in both directions.
type Amp struct{}

func init() { register(Amp{}) }

func (Amp) ID() string { return "amp" }

// ampSettingsPath resolves Amp's user settings file.
func ampSettingsPath() (string, error) {
	if v := os.Getenv("AMP_SETTINGS_FILE"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "amp", "settings.json"), nil
}

// ampPluginPath is the one file this adapter writes, for either scope.
//
// Amp reads plugins from `<config dir>/plugins/`, a sibling of settings.json,
// so it is derived from the settings path — which keeps AMP_SETTINGS_FILE the
// single override for "where Amp's config lives".
//
// Amp also reads a project-local `.amp/plugins/`, and this adapter
// deliberately does not write there for project scope. The file is executable
// code that shells out on every turn; committing it would run `lode hook` in
// the checkout of every contributor, including those with no `lode` installed
// and no interest in Worklode. As with codex, both scopes write the user-level
// file and the `lode hook` guard is what scopes behaviour to Worklode
// worktrees.
func ampPluginPath() (string, error) {
	settings, err := ampSettingsPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(settings), "plugins", "worklode.ts"), nil
}

// Detect: Amp is configured for the user (its settings file exists). The
// plugin directory is a sibling that need not exist yet, so the settings file
// stays the signal.
func (Amp) Detect(repoDir string) (bool, error) {
	path, err := ampSettingsPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	return err == nil, nil
}

// SkillTargets: ~/.agents/skills only. Amp's own `.amp/skills` is unverified,
// so v1 relies on the shared directory (spec 008 §17.3).
func (Amp) SkillTargets(repoDir, scope string) ([]SkillTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return []SkillTarget{{Dir: filepath.Join(home, ".agents", "skills")}}, nil
}

// Events is ampBindings read the other way round, so the event table cannot
// drift from what install actually writes (spec 008 §17.1).
func (Amp) Events() map[Event][]string { return eventsFor(ampBindings) }

// ampPluginSource renders the plugin file. It takes no arguments and depends
// on nothing but ampBindings, so two installs produce identical bytes.
func ampPluginSource() ([]byte, error) {
	for _, b := range ampBindings {
		if !ampCommandPattern.MatchString(b.Command) {
			return nil, fmt.Errorf("amp binding %q: command %q is not %s", b.Event, b.Command, ampCommandPattern)
		}
	}
	var buf bytes.Buffer
	if err := ampPlugin.Execute(&buf, struct{ Bindings []hookBinding }{ampBindings}); err != nil {
		return nil, fmt.Errorf("render amp plugin: %w", err)
	}
	return buf.Bytes(), nil
}

// InstallHooks writes the plugin file, which is ours entire. A re-install that
// would write the bytes already there writes nothing at all: the JSON adapters
// go out of their way not to bump a file's mtime for a no-op, and a file Amp
// watches deserves the same.
func (a Amp) InstallHooks(repoDir, scope string) (HookInstall, error) {
	path, err := ampPluginPath()
	if err != nil {
		return HookInstall{}, err
	}
	src, err := ampPluginSource()
	if err != nil {
		return HookInstall{}, err
	}
	if existing, err := os.ReadFile(path); err != nil || !bytes.Equal(existing, src) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return HookInstall{}, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, src, 0o644); err != nil {
			return HookInstall{}, fmt.Errorf("write %s: %w", path, err)
		}
	}
	return HookInstall{
		Path:    path,
		Bound:   boundNames(ampBindings),
		Unbound: missingEvents(a),
		Notes:   []string{ampCeilingNote, ampReloadNote},
	}, nil
}

// UninstallHooks deletes the generated plugin, and only that: a file at the
// same path without our marker is someone else's and is reported ActionNone
// rather than removed. A missing file is ActionNone too, not an error.
func (Amp) UninstallHooks(repoDir, scope string) (HookUninstall, error) {
	path, err := ampPluginPath()
	if err != nil {
		return HookUninstall{}, err
	}
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return HookUninstall{Path: path, Action: ActionNone}, nil
	}
	if err != nil {
		return HookUninstall{}, fmt.Errorf("read %s: %w", path, err)
	}
	if !bytes.Contains(existing, []byte(ampPluginMarker)) {
		return HookUninstall{Path: path, Action: ActionNone}, nil
	}
	if err := os.Remove(path); err != nil {
		return HookUninstall{}, fmt.Errorf("remove %s: %w", path, err)
	}
	return HookUninstall{Path: path, Action: ActionRemoved}, nil
}
