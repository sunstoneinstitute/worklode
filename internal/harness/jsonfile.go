package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReadJSONFile reads path as generic JSON. A missing file is an empty
// settings object, not an error — installing into a repo that has never had
// harness settings is the common case.
//
// It is the one exported member of this file: internal/cmd's install tests
// assert on what an adapter wrote by reading it back through the same reader
// that wrote it. Adapters aside, nothing else needs it.
func ReadJSONFile(path string) (map[string]any, error) {
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

// writeJSONFile writes settings back to path, creating the parent directory
// if needed. Output is indented and newline-terminated so a committed
// settings file stays readable and diffs cleanly.
//
// It is a var so tests can count writes: an adapter that owns two surfaces in
// one file (claude-code's hooks and status line) must fold them into a single
// read-modify-write, and the write count is the only way to observe that from
// outside.
var writeJSONFile = func(path string, settings map[string]any) error {
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
