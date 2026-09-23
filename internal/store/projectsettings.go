package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// allowedProjectSettings is the server-side allowlist of projects.settings
// keys (increment 3 R9). A key not listed is refused, so a typo cannot land.
// Sibling increments add their keys here; the value validator says what shape
// the key takes.
var allowedProjectSettings = map[string]func(json.RawMessage) error{
	"plan_tokens_soft": positiveInt,
	"plan_tokens_hard": positiveInt,
}

// positiveInt validates a projects.settings value that must be a positive
// integer (plan_tokens_soft, plan_tokens_hard).
func positiveInt(v json.RawMessage) error {
	var n int
	if err := json.Unmarshal(v, &n); err != nil || n <= 0 {
		return fmt.Errorf("want a positive integer, got %s", string(v))
	}
	return nil
}

// SetProjectSettings merges patch into projects.settings. A null value
// removes the key. Unknown keys and values of the wrong shape are refused
// with ErrInvalidInput naming the key, before anything is written.
func (s *Store) SetProjectSettings(ctx context.Context, projectID string, patch map[string]json.RawMessage) error {
	for k, v := range patch {
		check, ok := allowedProjectSettings[k]
		if !ok {
			return fmt.Errorf("unknown project setting %q: %w", k, ErrInvalidInput)
		}
		if string(v) == "null" {
			continue
		}
		if err := check(v); err != nil {
			return fmt.Errorf("project setting %q: %v: %w", k, err, ErrInvalidInput)
		}
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal settings patch: %w", err)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE projects SET settings = jsonb_strip_nulls(settings || $2::jsonb) WHERE id = $1`,
		projectID, raw)
	if err != nil {
		return fmt.Errorf("set settings of project %s: %w", projectID, err)
	}
	return requireOneAffected(res, "set project settings",
		fmt.Errorf("project %s: %w", projectID, ErrNotFound))
}
