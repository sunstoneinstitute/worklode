package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

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
var claudeBindings = []claudeBinding{
	{Event: "SessionStart", Command: "lode hook session-start"},
	{Event: "SessionEnd", Command: "lode hook session-end"},
	{Event: "Stop", Command: "lode hook heartbeat"},
	{Event: "StopFailure", Command: "lode hook heartbeat"},
	{Event: "SubagentStop", Command: "lode hook heartbeat"},
	{Event: "Notification", Command: "lode hook heartbeat"},
	{Event: "WorktreeCreate", Command: "lode hook worktree-create"},
	{Event: "WorktreeRemove", Command: "lode hook worktree-remove"},
	{Event: "PostToolUse", Matcher: "EnterWorktree", Command: "lode hook worktree-enter"},
}

func init() {
	rootCmd.AddCommand(newClaudeCmd())
}

func newClaudeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Manage Worklode's Claude Code integration",
	}
	cmd.AddCommand(newClaudeInstallCmd(), newClaudeUninstallCmd())
	return cmd
}

func newClaudeInstallCmd() *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Worklode's hooks into this repo's Claude Code settings",
		Long: "Writes Worklode's lifecycle hook bindings (session start/end, heartbeat, " +
			"worktree enter/create/remove) into the repo's Claude Code settings file. " +
			"Safe to re-run: it replaces Worklode's own entries and leaves every other " +
			"setting untouched.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := settingsPathForScope(scope)
			if err != nil {
				return err
			}
			if err := installClaudeHooks(path); err != nil {
				return err
			}
			return reportClaudeCmd(cmd, "installed", path)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", scopeLocal,
		"which settings file to write: local (settings.local.json) or project (settings.json)")
	return cmd
}

func newClaudeUninstallCmd() *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Worklode's hooks from this repo's Claude Code settings",
		Long: "Removes every `lode hook` binding from the repo's Claude Code settings file, " +
			"leaving all other settings — including third-party hooks on the same events — " +
			"in place. A missing settings file is not an error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := settingsPathForScope(scope)
			if err != nil {
				return err
			}
			if err := uninstallClaudeHooks(path); err != nil {
				return err
			}
			return reportClaudeCmd(cmd, "uninstalled", path)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", scopeLocal,
		"which settings file to write: local (settings.local.json) or project (settings.json)")
	return cmd
}

// reportClaudeCmd prints the outcome in whichever form the caller asked for.
func reportClaudeCmd(cmd *cobra.Command, action, path string) error {
	if jsonOut(cmd) {
		b, err := json.Marshal(struct {
			Action string `json:"action"`
			Path   string `json:"path"`
		}{Action: action, Path: path})
		if err != nil {
			return err
		}
		printRaw(cmd, b)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s Worklode hooks in %s\n", action, path)
	return nil
}

// settingsPathForScope resolves the settings file for scope, relative to the
// git worktree root of the current directory.
func settingsPathForScope(scope string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}
	root, ok := worktree.Root(cwd)
	if !ok {
		return "", fmt.Errorf("not inside a git repository: %s", cwd)
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
	hooks := stripLodeHooks(settingsHooks(settings))
	for _, b := range claudeBindings {
		hooks[b.Event] = appendBinding(hooks[b.Event], b)
	}
	settings["hooks"] = hooks
	return writeSettingsFile(path, settings)
}

// uninstallClaudeHooks removes Worklode's bindings from the settings file at
// path. A missing file is a no-op: there is nothing to uninstall.
func uninstallClaudeHooks(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	settings, err := readSettingsFile(path)
	if err != nil {
		return err
	}
	hooks := stripLodeHooks(settingsHooks(settings))
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	return writeSettingsFile(path, settings)
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
// third-party hook sharing an event is preserved.
func stripLodeHooks(hooks map[string]any) map[string]any {
	out := map[string]any{}
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
	return out
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
