package harness

import (
	"os"
	"strings"
)

// lodeHookPrefix marks a settings entry as Worklode's. JSON has no comments,
// so the command itself is the marker: install strips every entry with this
// prefix before writing the current set, which makes a re-run converge rather
// than duplicate.
const lodeHookPrefix = "lode hook "

// hookBinding is one harness event Worklode listens to. An empty Matcher means
// the binding applies to every occurrence of the event. Claude Code and Codex
// share this shape because they share the config shape it is written into.
type hookBinding struct {
	Event   string
	Matcher string
	Command string
}

// nativeName is how one binding is spelled outside the adapter: the harness's
// own event name, qualified by its matcher when it has one.
func nativeName(b hookBinding) string {
	if b.Matcher == "" {
		return b.Event
	}
	return b.Event + ":" + b.Matcher
}

// boundNames lists the harness-native event names an install writes.
func boundNames(bindings []hookBinding) []string {
	out := make([]string, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, nativeName(b))
	}
	return out
}

// bindingEvent reads the Worklode event out of a binding's command, which is
// always `lode hook <event>` plus optional flags.
func bindingEvent(b hookBinding) Event {
	rest := strings.TrimPrefix(b.Command, lodeHookPrefix)
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		rest = rest[:i]
	}
	return Event(rest)
}

// eventsFor reads a binding table the other way round: the command a binding
// runs names the Worklode event, so an adapter's event table cannot restate —
// and so cannot drift from — what install actually writes (spec 008 §17.1).
func eventsFor(bindings []hookBinding) map[Event][]string {
	out := map[Event][]string{}
	for _, b := range bindings {
		event := bindingEvent(b)
		out[event] = append(out[event], nativeName(b))
	}
	return out
}

// installGroupedHooks writes bindings into the JSON config at path, replacing
// any bindings a previous install left behind and preserving every other
// setting. "Grouped" is the event → matcher-group → handler shape Claude Code
// and Codex both use. A file that exists but does not parse returns the read
// error and is never rewritten (spec 024 acceptance 6).
func installGroupedHooks(path string, bindings []hookBinding) error {
	settings, err := ReadJSONFile(path)
	if err != nil {
		return err
	}
	hooks, _ := stripLodeHooks(settingsHooks(settings))
	for _, b := range bindings {
		hooks[b.Event] = appendBinding(hooks[b.Event], b)
	}
	settings["hooks"] = hooks
	return writeJSONFile(path, settings)
}

// uninstallGroupedHooks removes Worklode's bindings from the JSON config at
// path, reporting ActionNone or ActionRemoved (the same vocabulary the git
// hooks use). A missing file, or one with no `lode hook` entries to strip, is
// ActionNone and leaves the file untouched — a no-op must not reformat
// someone's settings JSON or bump its mtime.
func uninstallGroupedHooks(path string) (action string, err error) {
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
	if err := writeJSONFile(path, settings); err != nil {
		return "", err
	}
	return ActionRemoved, nil
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
func appendBinding(existing any, b hookBinding) []any {
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
