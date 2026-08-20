package harness

import (
	"os"
	"path/filepath"
)

// ampNoHooksNote explains an install that wrote nothing. Without it the
// report would look like a failure rather than the honest ceiling it is.
const ampNoHooksNote = "amp hook actions cannot run a shell command, so no lifecycle events are bound; " +
	"skills and the git pre-commit heartbeat still work"

// Amp is the amp adapter. Amp's hook actions are limited to sending a user
// message and redacting tool input — there is no shell action of any kind,
// and no session or stop event — so Amp cannot reach `lode hook` and this
// adapter binds nothing rather than writing an inert config. Its settings
// file is located (for the report and for detection) but never written.
//
// Amp's Plugin API can run shell commands, but it is TypeScript registered
// through amp.on(); that is a code-generating adapter, not a config-merging
// one, and is out of scope for v1.
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

// Detect: Amp is configured for the user (its settings file exists).
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

// Events is empty: no Amp event can run `lode hook`.
func (Amp) Events() map[Event][]string { return map[Event][]string{} }

// InstallHooks writes nothing and reports every Worklode event unbound —
// degraded coverage, not a failure (spec 008 §17.1).
func (a Amp) InstallHooks(repoDir, scope string) (HookInstall, error) {
	path, err := ampSettingsPath()
	if err != nil {
		return HookInstall{}, err
	}
	return HookInstall{Path: path, Unbound: missingEvents(a), Notes: []string{ampNoHooksNote}}, nil
}

// UninstallHooks has nothing to remove, so it never touches the file.
func (Amp) UninstallHooks(repoDir, scope string) (HookUninstall, error) {
	path, err := ampSettingsPath()
	if err != nil {
		return HookUninstall{}, err
	}
	return HookUninstall{Path: path, Action: ActionNone}, nil
}
