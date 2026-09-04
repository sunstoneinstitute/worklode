package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/pelletier/go-toml/v2"
)

// codexConfigPath resolves $CODEX_HOME/config.toml, the user-level file spec
// 063 §3 requires Codex's OTel export configuration to live in. Unlike
// hooks.json, this file is not reinstalled per worktree -- both Worklode
// install scopes write the same path, same as codexHooksPath.
func codexConfigPath() (string, error) {
	dir, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// codexOTLPEndpoint is the local collector lode-server runs, the same
// address Claude Code's telemetry env vars point at (internal/harness's
// claudecode.go).
const codexOTLPEndpoint = "http://127.0.0.1:4317"

// codexExporterValue is the exact value installCodexTelemetry writes under
// [otel].exporter. uninstallCodexTelemetry only removes an exporter entry
// that still equals this -- a value the user repointed elsewhere is theirs
// to keep.
func codexExporterValue() map[string]any {
	return map[string]any{
		"otlp-grpc": map[string]any{
			"endpoint": codexOTLPEndpoint,
		},
	}
}

// readCodexConfigFile reads path as generic TOML. A missing file is an
// empty config, not an error -- installing before Codex has ever written one
// is the common case. A file that exists but does not parse is returned as
// an error and never rewritten (spec 024 acceptance 6, the same contract
// codex.go's hooks.json reader holds).
func readCodexConfigFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var cfg map[string]any
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// writeCodexConfigFile writes cfg back to path atomically at mode 0600,
// following internal/cli.writeFileAtomic's temp-file, chmod, rename sequence
// reimplemented here rather than exported: a crash cannot leave a truncated
// config, and the mode is set on the temp file so an existing looser-mode
// file is replaced rather than written through.
func writeCodexConfigFile(path string, cfg map[string]any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, "config-*.toml")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename succeeds
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// installCodexTelemetry merges Worklode's OTel export settings into path's
// [otel] table, preserving every unrelated top-level key and every unrelated
// key already under [otel] (such as environment). Metrics and traces stay
// off; only the log exporter Codex needs for completed-response token counts
// is configured, and log_user_prompt is pinned false so no prompt content is
// ever exported (spec 063 §3).
func installCodexTelemetry(path string) (string, error) {
	cfg, err := readCodexConfigFile(path)
	if err != nil {
		return "", err
	}
	otel, ok := cfg["otel"].(map[string]any)
	if !ok {
		otel = map[string]any{}
	}
	otel["log_user_prompt"] = false
	otel["exporter"] = codexExporterValue()
	cfg["otel"] = otel
	if err := writeCodexConfigFile(path, cfg); err != nil {
		return "", err
	}
	return ActionInstalled, nil
}

// uninstallCodexTelemetry removes only the [otel] keys installCodexTelemetry
// owns, and only where they still hold Worklode's own values: exporter when
// it still equals codexExporterValue(), log_user_prompt when it is still
// false -- Codex's own default, so removing it cannot enable prompt content.
// A user-modified value is left in place. An [otel] table left empty by the
// removal is dropped entirely. A missing file is a successful no-op.
func uninstallCodexTelemetry(path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ActionNone, nil
	}
	cfg, err := readCodexConfigFile(path)
	if err != nil {
		return "", err
	}
	otel, ok := cfg["otel"].(map[string]any)
	if !ok {
		return ActionNone, nil
	}
	changed := false
	if exporter, ok := otel["exporter"]; ok && reflect.DeepEqual(exporter, codexExporterValue()) {
		delete(otel, "exporter")
		changed = true
	}
	if v, ok := otel["log_user_prompt"]; ok && v == false {
		delete(otel, "log_user_prompt")
		changed = true
	}
	if !changed {
		return ActionNone, nil
	}
	if len(otel) == 0 {
		delete(cfg, "otel")
	} else {
		cfg["otel"] = otel
	}
	if err := writeCodexConfigFile(path, cfg); err != nil {
		return "", err
	}
	return ActionRemoved, nil
}
