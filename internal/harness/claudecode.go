package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/cli"
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
	settings, err := ReadJSONFile(path)
	if err != nil {
		return HookInstall{}, err
	}
	applyGroupedHooks(settings, claudeBindings)
	projectID, taskID := resolveClaudeTelemetryIDs(repoDir)
	applyClaudeTelemetry(settings, projectID, taskID, cli.ServerURLFrom(repoDir))
	if err := writeJSONFile(path, settings); err != nil {
		return HookInstall{}, err
	}
	return HookInstall{Path: path, Bound: boundNames(claudeBindings)}, nil
}

// UninstallHooks removes Worklode's bindings from the scope's settings file
// for the repo containing repoDir, plus the telemetry env vars and resource
// attributes InstallHooks writes -- the same single read-modify-write
// InstallHooks uses to write them.
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
	projectID, taskID := resolveClaudeTelemetryIDs(repoDir)
	applyClaudeTelemetry(settings, projectID, taskID, cli.ServerURLFrom(repoDir))
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
	// the developer's own hooks, their status line and otelHeadersHelper — is
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
	projectID, taskID := resolveClaudeTelemetryIDs(dir)
	applyClaudeTelemetry(settings, projectID, taskID, cli.ServerURLFrom(dir))
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

// claudeTelemetryEnv is the fixed set of environment variables a Claude Code
// install writes to turn on OTel-based usage telemetry: metrics go to the
// local Edge Agent collector at OTEL_EXPORTER_OTLP_ENDPOINT, not lode-server.
// Log events go to Worklode instead, over the separate keys claudeLogsEnv and
// applyClaudeTelemetry's serverURL parameter write. The values never vary by
// project or scope, so there is nothing here for a caller to configure.
var claudeTelemetryEnv = map[string]string{
	"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
	"OTEL_METRICS_EXPORTER":        "otlp",
	"OTEL_EXPORTER_OTLP_PROTOCOL":  "grpc",
	"OTEL_EXPORTER_OTLP_ENDPOINT":  "http://127.0.0.1:4317",
}

// claudeLogsEnv is the fixed half of the env vars that turn on Claude Code's
// OTel log exporter and point it at Worklode. The endpoint is the other half
// -- it varies by server URL, so applyClaudeTelemetry computes and writes it
// separately, and both apply and strip are written only when a server URL
// resolves: a logs exporter with no endpoint would spam errors on every
// export attempt.
var claudeLogsEnv = map[string]string{
	"OTEL_LOGS_EXPORTER":               "otlp",
	"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json",
}

// otelLogsEndpointKey is the env var holding the logs exporter's endpoint;
// its value is always serverURL + otelLogsPath.
const otelLogsEndpointKey = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"

const otelLogsPath = "/otlp/v1/logs"

// otelHeadersHelperCommand is the value ClaudeCode.InstallHooks writes into
// Claude Code's top-level otelHeadersHelper setting -- the credential helper
// Claude Code runs before every OTLP export to get the bearer token for the
// log exporter, since the token itself never lands in a settings file. Also
// how uninstall recognizes its own entry.
const otelHeadersHelperCommand = "lode-hook otel-headers"

// The two OTEL_RESOURCE_ATTRIBUTES keys Worklode owns. Every other key=value
// entry in that comma-separated string belongs to someone else and is
// preserved untouched by both apply and strip.
const (
	resourceProjectKey = "worklode.project.id"
	resourceTaskKey    = "worklode.task.id"
)

// settingsEnv returns the settings' "env" object, or an empty one when it is
// absent or not an object -- the same convention settingsHooks uses.
func settingsEnv(settings map[string]any) map[string]any {
	env, ok := settings["env"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return env
}

// resolveClaudeTelemetryIDs resolves the project and task identity a Claude
// Code install stamps into OTEL_RESOURCE_ATTRIBUTES. The project comes from
// local config only -- the same cheap, no-server-round-trip contract
// CurrentProjectFrom promises internal/hookrun -- and the task comes only
// from the enclosing worktree's own git-config stamp, never the directory
// name. A main checkout is never stamped, which is what makes its own
// install carry no task attribute rather than copying one from wherever the
// process happens to be invoked.
func resolveClaudeTelemetryIDs(repoDir string) (projectID, taskID string) {
	projectID = cli.CurrentProjectFrom(repoDir)
	if root, ok := worktree.Root(repoDir); ok {
		taskID, _ = worktree.StampedTaskID(root)
	}
	return projectID, taskID
}

// mergeResourceAttributes merges Worklode's project and task identity into an
// existing OTEL_RESOURCE_ATTRIBUTES value, keeping every other key=value
// entry untouched. Entries are sorted so a second install with the same
// inputs writes the exact same string. An empty id is left out rather than
// written as "worklode.task.id=" -- that is both how a main checkout's
// install ends up with no task attribute, and how a strip (called with "",
// "") removes both keys while leaving the rest alone.
func mergeResourceAttributes(existing, projectID, taskID string) string {
	attrs := map[string]string{}
	for _, part := range strings.Split(existing, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		attrs[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	delete(attrs, resourceProjectKey)
	delete(attrs, resourceTaskKey)
	if projectID != "" {
		attrs[resourceProjectKey] = projectID
	}
	if taskID != "" {
		attrs[resourceTaskKey] = taskID
	}
	if len(attrs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+attrs[k])
	}
	return strings.Join(parts, ",")
}

// hasResourceKey reports whether an OTEL_RESOURCE_ATTRIBUTES value carries
// key among its comma-separated key=value entries.
func hasResourceKey(attrs, key string) bool {
	for _, part := range strings.Split(attrs, ",") {
		k, _, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.TrimSpace(k) == key {
			return true
		}
	}
	return false
}

// applyClaudeTelemetry sets Worklode's telemetry env vars on an already-read
// settings object and merges the project/task resource attributes into
// OTEL_RESOURCE_ATTRIBUTES, preserving every foreign env key and resource
// attribute. It mutates settings in place and never touches the filesystem --
// the same contract applyGroupedHooks has, so a caller with more than one
// surface in the file folds this into its own single read-modify-write.
//
// When serverURL is non-empty it also turns on the log exporter (claudeLogsEnv
// plus the computed endpoint) and points otelHeadersHelper at lode-hook.
// serverURL == "" writes none of that -- an unresolved server means no place
// to send logs -- and leaves the metrics keys above as the only telemetry
// this install turns on.
func applyClaudeTelemetry(settings map[string]any, projectID, taskID, serverURL string) {
	env := settingsEnv(settings)
	for k, v := range claudeTelemetryEnv {
		env[k] = v
	}
	existing, _ := env["OTEL_RESOURCE_ATTRIBUTES"].(string)
	if merged := mergeResourceAttributes(existing, projectID, taskID); merged != "" {
		env["OTEL_RESOURCE_ATTRIBUTES"] = merged
	} else {
		delete(env, "OTEL_RESOURCE_ATTRIBUTES")
	}
	if serverURL != "" {
		for k, v := range claudeLogsEnv {
			env[k] = v
		}
		env[otelLogsEndpointKey] = serverURL + otelLogsPath
		applyOTelHeadersHelper(settings)
	}
	settings["env"] = env
}

// applyOTelHeadersHelper points Claude Code's otelHeadersHelper setting at
// lode-hook, mirroring applyStatusLine's rule for its own single-command
// slot: claim it only when it is empty or already Worklode's, so a developer's
// own helper is never overwritten.
func applyOTelHeadersHelper(settings map[string]any) {
	if existing, ok := settings["otelHeadersHelper"]; ok && existing != otelHeadersHelperCommand {
		return
	}
	settings["otelHeadersHelper"] = otelHeadersHelperCommand
}

// stripClaudeTelemetry removes Worklode's telemetry env vars from an
// already-read settings object, but only the exact vars and only where they
// still hold Worklode's own values -- a developer who repointed one elsewhere
// keeps their own value. The logs endpoint is checked by suffix rather than
// exact match, since its value carries the server URL: it is Worklode's iff
// it ends with otelLogsPath. It always drops the two worklode.* resource
// attributes it owns when present, and the otelHeadersHelper entry when it is
// still lode-hook's, preserving every other env key, resource attribute and
// foreign helper. Returns ActionRemoved or ActionNone, stripGroupedHooks'
// vocabulary, so a caller with more than one surface can OR the results
// together to decide whether to write.
func stripClaudeTelemetry(settings map[string]any) (action string) {
	changed := false

	if env, ok := settings["env"].(map[string]any); ok {
		before := len(env)
		for k, want := range claudeTelemetryEnv {
			if got, ok := env[k].(string); ok && got == want {
				delete(env, k)
				changed = true
			}
		}
		for k, want := range claudeLogsEnv {
			if got, ok := env[k].(string); ok && got == want {
				delete(env, k)
				changed = true
			}
		}
		if got, ok := env[otelLogsEndpointKey].(string); ok && strings.HasSuffix(got, otelLogsPath) {
			delete(env, otelLogsEndpointKey)
			changed = true
		}
		if existing, ok := env["OTEL_RESOURCE_ATTRIBUTES"].(string); ok {
			if hasResourceKey(existing, resourceProjectKey) || hasResourceKey(existing, resourceTaskKey) {
				changed = true
				if merged := mergeResourceAttributes(existing, "", ""); merged == "" {
					delete(env, "OTEL_RESOURCE_ATTRIBUTES")
				} else {
					env["OTEL_RESOURCE_ATTRIBUTES"] = merged
				}
			}
		}
		// Only an env this strip emptied goes away. A settings file that
		// already carried "env": {} keeps it, since an uninstall writing for
		// some other reason must not drop a key it did not touch.
		if len(env) == 0 && before > 0 {
			delete(settings, "env")
		} else {
			settings["env"] = env
		}
	}

	if stripOTelHeadersHelper(settings) {
		changed = true
	}

	if !changed {
		return ActionNone
	}
	return ActionRemoved
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
