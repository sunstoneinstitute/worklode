package secrets

import (
	"errors"
	"fmt"
	"os"

	"github.com/zalando/go-keyring"
)

// Service returns the OS-keystore service name for a task. One service per
// task, one item per secret name: reads are scoped to exactly the task's
// materialized set, and purging a task cannot touch another task's items.
func Service(taskID string) string { return "worklode:" + taskID }

// ErrNotStored reports a secret name with no keystore item.
var ErrNotStored = errors.New("secret not in keystore")

// checkItem gates the keystore coordinates. The name reaches an exec child's
// environment as NAME=value, so a malformed one is an injection, and the task
// id scopes every item — both are validated before any keystore call.
func checkItem(taskID, name string) error {
	if !ValidTaskID(taskID) {
		return fmt.Errorf("invalid task id %q", taskID)
	}
	if !ValidName(name) {
		return fmt.Errorf("invalid secret name %q", name)
	}
	return nil
}

// Put stores one secret value for a task. The value comes from the op-run
// child environment (see `lode secrets pack`) and goes nowhere else.
//
// OS keystores cap an item's size — macOS at roughly 2.9 KB of raw value
// (security(1) rejects an add-generic-password command over 4096 bytes, and
// go-keyring base64s the value first), Windows at 2560. A value over the cap
// is a catalog-modelling error rather than a keystore bug: an asset that big
// is a credential wrapped in non-secret scaffolding, and only the credential
// belongs here. Say so, because the backend's own error says only "data
// passed to Set was too big".
func Put(taskID, name, value string) error {
	if err := checkItem(taskID, name); err != nil {
		return err
	}
	if err := keyring.Set(Service(taskID), name, value); err != nil {
		if errors.Is(err, keyring.ErrSetDataTooBig) {
			return fmt.Errorf("keystore set %s for %s: the value is %d bytes and this OS "+
				"keystore caps an item at ~2.5-3 KB; split the catalog entry so only the "+
				"credential is a secret and the non-secret remainder ships as a plaintext "+
				"template: %w", name, taskID, len(value), err)
		}
		return fmt.Errorf("keystore set %s for %s: %w", name, taskID, err)
	}
	return nil
}

// Fetch reads one secret value for a task. A missing item is ErrNotStored.
func Fetch(taskID, name string) (string, error) {
	if err := checkItem(taskID, name); err != nil {
		return "", err
	}
	v, err := keyring.Get(Service(taskID), name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("%s for %s: %w", name, taskID, ErrNotStored)
	}
	if err != nil {
		return "", fmt.Errorf("keystore get %s for %s: %w", name, taskID, err)
	}
	return v, nil
}

// Del removes one secret item; a missing item is a no-op so purge paths are
// idempotent.
func Del(taskID, name string) error {
	if err := checkItem(taskID, name); err != nil {
		return err
	}
	err := keyring.Delete(Service(taskID), name)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("keystore delete %s for %s: %w", name, taskID, err)
	}
	return nil
}

// PurgeTask removes every rendered file and keystore item recorded in the
// task's manifest and the manifest itself, returning the removed entry names.
// keyring has no enumeration API, so the manifest is the authority on what to
// remove; no manifest means nothing to purge.
//
// Rendered files go first and by absolute path, so `--task` purges them from
// anywhere (spec 042 §4.1); a file already gone is fine.
func PurgeTask(taskID string) ([]string, error) {
	if !ValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id %q", taskID)
	}
	m, ok := LoadManifest(taskID)
	if !ok {
		return nil, nil
	}
	for _, e := range m.Entries {
		if e.Rendered == "" {
			continue
		}
		if err := os.Remove(e.Rendered); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove rendered %s: %w", e.Rendered, err)
		}
	}
	// A pre-042 manifest records no entries; its materialized names are its
	// item names, which is exactly what AllItems degrades to being empty for.
	items := m.AllItems()
	if len(items) == 0 {
		items = m.Materialized
	}
	for _, n := range items {
		if err := Del(taskID, n); err != nil {
			return nil, err
		}
	}
	if err := RemoveManifest(taskID); err != nil {
		return nil, err
	}
	return m.Materialized, nil
}
