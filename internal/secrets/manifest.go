package secrets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Manifest records WHICH secret names a task has materialized or declined —
// names only, never values or op:// refs. It lives outside the worktree
// (~/.cache/worklode/secrets/<task-id>.json) because purge must still work
// after the worktree is deleted, and keyring cannot enumerate its own items.
type Manifest struct {
	Task         string    `json:"task"`
	Materialized []string  `json:"materialized,omitempty"`
	Declined     []string  `json:"declined,omitempty"`
	At           time.Time `json:"at"`
}

// manifestPath returns ~/.cache/worklode/secrets/<taskID>.json.
func manifestPath(taskID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "worklode", "secrets", taskID+".json"), nil
}

// LoadManifest reads a task's manifest; a missing or unreadable file is
// ok=false, never an error.
func LoadManifest(taskID string) (Manifest, bool) {
	path, err := manifestPath(taskID)
	if err != nil {
		return Manifest{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, false
	}
	var m Manifest
	if json.Unmarshal(data, &m) != nil {
		return Manifest{}, false
	}
	return m, true
}

// SaveManifest writes a task's manifest with 0600 permissions.
func SaveManifest(m Manifest) error {
	path, err := manifestPath(m.Task)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// RemoveManifest deletes a task's manifest; missing is fine.
func RemoveManifest(taskID string) error {
	path, err := manifestPath(taskID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
