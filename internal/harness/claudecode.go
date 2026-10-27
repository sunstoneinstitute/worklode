package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
// a human. SubagentStart carries the instruction to cite rules by ref into
// each subagent (WL-REQ-1791).
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
	{Event: "SubagentStart", Command: "lode-hook subagent-start"},
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
// (WL-REQ-276). Claude Code reads no project-scope shared dir.
func (ClaudeCode) SkillTargets(repoDir, scope string) ([]SkillTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return []SkillTarget{{Dir: filepath.Join(home, ".claude", "skills"), PerSkill: true}}, nil
}

// Events is claudeBindings read the other way round, so the event table
// cannot drift from what install actually writes (WL-REQ-274).
func (ClaudeCode) Events() map[Event][]string { return eventsFor(claudeBindings) }

// InstallHooks writes Worklode's bindings into the scope's settings file for
// the repo containing repoDir. Every Worklode event claude-code can express
// is bound, so nothing is reported unbound.
func (ClaudeCode) InstallHooks(repoDir, scope string) (HookInstall, error) {
	path, err := settingsPathForScope(repoDir, scope)
	if err != nil {
		return HookInstall{}, err
	}
	settings, err := ReadJSONFile(path)
	if err != nil {
		return HookInstall{}, err
	}
	applyGroupedHooks(settings, claudeBindings)
	applyClaudeTelemetry(settings)
	if err := writeJSONFile(path, settings); err != nil {
		return HookInstall{}, err
	}
	return HookInstall{Path: path, Bound: boundNames(claudeBindings)}, nil
}

// UninstallHooks removes Worklode's bindings from the scope's settings file
// for the repo containing repoDir, plus the telemetry settings InstallHooks
// writes -- the same single read-modify-write InstallHooks uses to write them.
func (ClaudeCode) UninstallHooks(repoDir, scope string) (HookUninstall, error) {
	path, err := settingsPathForScope(repoDir, scope)
	if err != nil {
		return HookUninstall{}, err
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return HookUninstall{Path: path, Action: ActionNone}, nil
	}
	settings, err := ReadJSONFile(path)
	if err != nil {
		return HookUninstall{}, err
	}
	hooks := stripGroupedHooks(settings)
	telemetry := stripClaudeTelemetry(settings)
	if hooks == ActionRemoved || telemetry == ActionRemoved {
		if err := writeJSONFile(path, settings); err != nil {
			return HookUninstall{}, err
		}
	}
	return HookUninstall{Path: path, Action: hooks}, nil
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
	applyClaudeTelemetry(settings)
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
	telemetry := stripClaudeTelemetry(settings)
	if hooks == ActionRemoved || statusLine == ActionRemoved || telemetry == ActionRemoved {
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
// worktree whose hooks were removed.
//
// This only ever mirrors a choice the developer already made at root: a repo
// where `lode install` was never run locally is left alone, so `lode work next`
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
	// the developer's own hooks and their status line — is
	// what an agent in the worktree would otherwise be missing. Copied only
	// where the worktree is silent, so its own edits survive. That includes
	// both single-command slots: mirroring the command the developer chose at
	// root is not the theft applyStatusLine and applyOTelHeadersHelper refuse
	// — those two guard the slot against Worklode's own command, and both
	// still run below over whatever the copy left.
	for k, v := range rootSettings {
		if _, ok := settings[k]; !ok {
			settings[k] = v
		}
	}
	applyGroupedHooks(settings, claudeBindings)
	// Re-applied rather than left as copied, so a root still carrying an
	// older form of our status line lands here as the current command.
	if sl, ok := rootSettings["statusLine"]; ok && isLodeStatusLine(sl) {
		applyStatusLine(settings)
	}
	applyClaudeTelemetry(settings)
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

// claudeTelemetryEnv is what a Claude Code install writes into the settings'
// env block: the vcs.* repository attributes on every metric and event, which
// is how telemetry is attributed to a repo. The exporters, endpoints and
// headers arrive through MDM-managed settings, and Claude Code ignores them in
// project and local settings anyway (v2.1.282). OTEL_RESOURCE_ATTRIBUTES is
// left alone: managed settings are likely to set it, and a project value
// would replace theirs.
var claudeTelemetryEnv = map[string]string{
	"OTEL_METRICS_INCLUDE_REPOSITORY": "true",
}

// legacyTelemetryEnv is what earlier installs wrote into project and local
// settings and Claude Code now ignores there. Install and uninstall both
// strip each var while it still holds the value Worklode wrote.
var legacyTelemetryEnv = map[string]string{
	"CLAUDE_CODE_ENABLE_TELEMETRY":     "1",
	"OTEL_METRICS_EXPORTER":            "otlp",
	"OTEL_EXPORTER_OTLP_PROTOCOL":      "grpc",
	"OTEL_EXPORTER_OTLP_ENDPOINT":      "http://127.0.0.1:4317",
	"OTEL_LOGS_EXPORTER":               "otlp",
	"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json",
}

// legacyLogsEndpointKey held serverURL + legacyLogsPath in earlier installs;
// the value is Worklode's iff it ends with legacyLogsPath.
const (
	legacyLogsEndpointKey = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
	legacyLogsPath        = "/otlp/v1/logs"
)

// legacyResourceKeys are the OTEL_RESOURCE_ATTRIBUTES entries earlier
// installs stamped. Every other key=value entry belongs to someone else.
var legacyResourceKeys = []string{"worklode.project.id", "worklode.task.id"}

// otelHeadersHelperCommand is the otelHeadersHelper earlier installs wrote.
// Install and uninstall remove it while it still holds this value.
const otelHeadersHelperCommand = "lode-hook otel-headers"

// settingsEnv returns the settings' "env" object, or an empty one when it is
// absent or not an object -- the same convention settingsHooks uses.
func settingsEnv(settings map[string]any) map[string]any {
	env, ok := settings["env"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return env
}

// applyClaudeTelemetry writes claudeTelemetryEnv into an already-read
// settings object and strips what earlier installs left behind, preserving
// every foreign env key and a developer's own otelHeadersHelper. It mutates
// settings in place and never touches the filesystem, so a caller folds it
// into its own single read-modify-write.
func applyClaudeTelemetry(settings map[string]any) {
	stripTelemetryEnv(settings, false)
	stripOTelHeadersHelper(settings)
	env := settingsEnv(settings)
	for k, v := range claudeTelemetryEnv {
		env[k] = v
	}
	settings["env"] = env
}

// stripClaudeTelemetry removes everything applyClaudeTelemetry and earlier
// installs wrote, but only where it still holds Worklode's value, plus
// otelHeadersHelper while it is still lode-hook's. Returns ActionRemoved or
// ActionNone, stripGroupedHooks' vocabulary, so a caller with more than one
// surface can OR the results together to decide whether to write.
func stripClaudeTelemetry(settings map[string]any) (action string) {
	changed := stripTelemetryEnv(settings, true)
	if stripOTelHeadersHelper(settings) {
		changed = true
	}
	if !changed {
		return ActionNone
	}
	return ActionRemoved
}

// stripTelemetryEnv deletes what earlier installs wrote into env -- each
// legacyTelemetryEnv var still holding Worklode's value, the logs endpoint
// and the legacyResourceKeys -- and, when current is set, the
// claudeTelemetryEnv vars too. Only an env this strip emptied goes away: a
// settings file that already carried "env": {} keeps it.
func stripTelemetryEnv(settings map[string]any, current bool) bool {
	env, ok := settings["env"].(map[string]any)
	if !ok {
		return false
	}
	before := len(env)
	changed := false
	drop := func(vars map[string]string) {
		for k, want := range vars {
			if got, ok := env[k].(string); ok && got == want {
				delete(env, k)
				changed = true
			}
		}
	}
	drop(legacyTelemetryEnv)
	if current {
		drop(claudeTelemetryEnv)
	}
	if got, ok := env[legacyLogsEndpointKey].(string); ok && strings.HasSuffix(got, legacyLogsPath) {
		delete(env, legacyLogsEndpointKey)
		changed = true
	}
	if attrs, ok := env["OTEL_RESOURCE_ATTRIBUTES"].(string); ok {
		if kept, removed := dropResourceKeys(attrs, legacyResourceKeys); removed {
			changed = true
			if kept == "" {
				delete(env, "OTEL_RESOURCE_ATTRIBUTES")
			} else {
				env["OTEL_RESOURCE_ATTRIBUTES"] = kept
			}
		}
	}
	if len(env) == 0 && before > 0 {
		delete(settings, "env")
	}
	return changed
}

// dropResourceKeys removes keys from a comma-separated key=value
// OTEL_RESOURCE_ATTRIBUTES value, keeping every other entry in order, and
// reports whether it removed any.
func dropResourceKeys(attrs string, keys []string) (kept string, removed bool) {
	var parts []string
	for _, part := range strings.Split(attrs, ",") {
		k, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		if slices.Contains(keys, strings.TrimSpace(k)) {
			removed = true
			continue
		}
		if strings.TrimSpace(part) != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ","), removed
}

// stripOTelHeadersHelper removes settings' otelHeadersHelper entry, but only
// while it still holds otelHeadersHelperCommand -- a developer's own helper
// is left alone, mirroring stripStatusLine.
func stripOTelHeadersHelper(settings map[string]any) bool {
	existing, ok := settings["otelHeadersHelper"]
	if !ok || existing != otelHeadersHelperCommand {
		return false
	}
	delete(settings, "otelHeadersHelper")
	return true
}
