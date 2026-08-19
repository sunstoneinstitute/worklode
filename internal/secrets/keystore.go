package secrets

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// Service returns the OS-keystore service name for a task. One service per
// task, one item per secret name: reads are scoped to exactly the task's
// materialized set, and purging a task cannot touch another task's items.
func Service(taskID string) string { return "worklode:" + taskID }

// ErrNotStored reports a secret name with no keystore item.
var ErrNotStored = errors.New("secret not in keystore")

// Put stores one secret value for a task. The value comes from the op-run
// child environment (see `lode secrets pack`) and goes nowhere else.
func Put(taskID, name, value string) error {
	if err := keyring.Set(Service(taskID), name, value); err != nil {
		return fmt.Errorf("keystore set %s for %s: %w", name, taskID, err)
	}
	return nil
}

// Fetch reads one secret value for a task. A missing item is ErrNotStored.
func Fetch(taskID, name string) (string, error) {
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
	err := keyring.Delete(Service(taskID), name)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("keystore delete %s for %s: %w", name, taskID, err)
	}
	return nil
}

// PurgeTask removes every keystore item recorded in the task's manifest and
// the manifest itself, returning the removed names. keyring has no
// enumeration API, so the manifest is the authority on what to remove; no
// manifest means nothing to purge.
func PurgeTask(taskID string) ([]string, error) {
	m, ok := LoadManifest(taskID)
	if !ok {
		return nil, nil
	}
	for _, n := range m.Materialized {
		if err := Del(taskID, n); err != nil {
			return nil, err
		}
	}
	if err := RemoveManifest(taskID); err != nil {
		return nil, err
	}
	return m.Materialized, nil
}
