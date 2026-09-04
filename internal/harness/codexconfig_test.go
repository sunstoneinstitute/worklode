package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// readCodexConfig decodes the TOML file at path, the same way
// readCodexConfigFile does, for tests to assert on.
func readCodexConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg map[string]any
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return cfg
}

func TestCodexTelemetryRoundTripPreservesForeignConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	seed := "model = \"gpt-5.6-sol\"\n[otel]\nenvironment = \"dev\"\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexTelemetry(path); err != nil {
		t.Fatal(err)
	}
	got := readCodexConfig(t, path)
	otel := got["otel"].(map[string]any)
	if got["model"] != "gpt-5.6-sol" || otel["environment"] != "dev" {
		t.Fatalf("foreign config lost: %#v", got)
	}
	if otel["log_user_prompt"] != false {
		t.Fatalf("log_user_prompt = %v, want false", otel["log_user_prompt"])
	}
}

// installCodexTelemetry must never enable metrics or traces, and the exact
// exporter shape spec 063 §3 requires must be present.
func TestCodexTelemetryInstallWritesExporter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	action, err := installCodexTelemetry(path)
	if err != nil {
		t.Fatal(err)
	}
	if action != ActionInstalled {
		t.Fatalf("action = %s, want %s", action, ActionInstalled)
	}
	got := readCodexConfig(t, path)
	otel := got["otel"].(map[string]any)
	if _, ok := otel["metrics_exporter"]; ok {
		t.Fatalf("metrics exporter enabled: %#v", otel)
	}
	if _, ok := otel["traces_exporter"]; ok {
		t.Fatalf("traces exporter enabled: %#v", otel)
	}
	exporter, ok := otel["exporter"].(map[string]any)
	if !ok {
		t.Fatalf("exporter missing or wrong shape: %#v", otel)
	}
	grpc, ok := exporter["otlp-grpc"].(map[string]any)
	if !ok || grpc["endpoint"] != "http://127.0.0.1:4317" {
		t.Fatalf("exporter.otlp-grpc = %#v", exporter)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// A missing config file is a successful no-op for uninstall, not an error --
// installing was never run, so there is nothing to remove.
func TestCodexTelemetryUninstallMissingFileIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	action, err := uninstallCodexTelemetry(path)
	if err != nil {
		t.Fatal(err)
	}
	if action != ActionNone {
		t.Fatalf("action = %s, want %s", action, ActionNone)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("uninstall on a missing file created one")
	}
}

// A file that exists but does not parse is returned as an error and never
// rewritten, mirroring the hooks.json contract (spec 024 acceptance 6).
func TestCodexTelemetryRefusesUnparseableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	bad := "this is not [ valid toml"
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexTelemetry(path); err == nil {
		t.Fatal("unparseable config was accepted by install")
	}
	if _, err := uninstallCodexTelemetry(path); err == nil {
		t.Fatal("unparseable config was accepted by uninstall")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != bad {
		t.Fatalf("unparseable config was rewritten: %s", data)
	}
}

// Uninstall only removes values that still match what Worklode wrote. A
// user who repointed the exporter elsewhere, or explicitly set
// log_user_prompt = true, keeps their own value, and log_user_prompt = false
// left in place by a foreign hand is never Worklode's to remove -- only the
// install call may have written it.
func TestCodexTelemetryUninstallKeepsUserModifiedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := installCodexTelemetry(path); err != nil {
		t.Fatal(err)
	}
	got := readCodexConfig(t, path)
	otel := got["otel"].(map[string]any)
	otel["exporter"] = map[string]any{"otlp-http": map[string]any{"endpoint": "https://example.com"}}
	data, err := toml.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	action, err := uninstallCodexTelemetry(path)
	if err != nil {
		t.Fatal(err)
	}
	if action != ActionRemoved {
		t.Fatalf("action = %s, want %s (log_user_prompt still removable)", action, ActionRemoved)
	}
	after := readCodexConfig(t, path)
	otelAfter := after["otel"].(map[string]any)
	exporter, ok := otelAfter["exporter"].(map[string]any)
	if !ok {
		t.Fatalf("user-modified exporter was removed: %#v", otelAfter)
	}
	http, ok := exporter["otlp-http"].(map[string]any)
	if !ok || http["endpoint"] != "https://example.com" {
		t.Fatalf("user-modified exporter changed: %#v", exporter)
	}
	if _, ok := otelAfter["log_user_prompt"]; ok {
		t.Fatalf("log_user_prompt (still Worklode's value) was not removed: %#v", otelAfter)
	}
}

// Removing an empty [otel] table entirely keeps a bare config from
// accumulating a stray header once every key under it is gone.
func TestCodexTelemetryUninstallDropsEmptyOtelTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := installCodexTelemetry(path); err != nil {
		t.Fatal(err)
	}
	if _, err := uninstallCodexTelemetry(path); err != nil {
		t.Fatal(err)
	}
	got := readCodexConfig(t, path)
	if _, ok := got["otel"]; ok {
		t.Fatalf("empty [otel] table left behind: %#v", got)
	}
}
