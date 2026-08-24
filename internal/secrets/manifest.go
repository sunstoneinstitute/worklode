package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// manifestDir returns ~/.cache/worklode/secrets, the directory holding one
// manifest per task with materialized secrets.
func manifestDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "worklode", "secrets"), nil
}

// manifestPath returns ~/.cache/worklode/secrets/<taskID>.json. The id is
// validated first: it is a path segment, so an id carrying ".." would let the
// callers read, write, and unlink outside the secrets directory.
func manifestPath(taskID string) (string, error) {
	if !ValidTaskID(taskID) {
		return "", fmt.Errorf("invalid task id %q", taskID)
	}
	dir, err := manifestDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, taskID+".json"), nil
}

// MaterializedTasks lists every task id with a local manifest, sorted. The
// keystore cannot enumerate its own items, so this directory is the machine's
// only inventory of materialized secrets, and a machine-wide sweep (017 §4)
// has nothing else to walk.
//
// A missing directory means nothing was ever materialized, not an error.
// Entries that are not a `<valid-task-id>.json` file are skipped rather than
// reported: the directory is under the user's control, a stray file there is
// not a failure of anyone's setup, and an id that would not survive
// ValidTaskID must never reach a keystore or path call.
func MaterializedTasks() ([]string, error) {
	dir, err := manifestDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read secrets directory %s: %w", dir, err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if ok && ValidTaskID(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
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
	for _, n := range slices.Concat(m.Materialized, m.Declined) {
		if !ValidName(n) {
			return fmt.Errorf("invalid secret name %q", n)
		}
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
