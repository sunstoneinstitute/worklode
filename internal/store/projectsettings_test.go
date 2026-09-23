package store

import (
	"encoding/json"
	"errors"
	"testing"
)

// rawSettings is a shorthand for building a SetProjectSettings patch.
func rawSettings(t *testing.T, kv map[string]any) map[string]json.RawMessage {
	t.Helper()
	patch := make(map[string]json.RawMessage, len(kv))
	for k, v := range kv {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %q: %v", k, err)
		}
		patch[k] = b
	}
	return patch
}

func TestSetProjectSettings(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "proj", "Proj", "PRJ"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := s.SetProjectSettings(ctx, "proj", rawSettings(t, map[string]any{
		"plan_tokens_soft": 32000,
		"plan_tokens_hard": 64000,
	})); err != nil {
		t.Fatalf("SetProjectSettings: %v", err)
	}

	got, err := s.GetProject(ctx, "proj")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if string(got.Settings["plan_tokens_soft"]) != "32000" {
		t.Errorf("plan_tokens_soft = %s, want 32000", got.Settings["plan_tokens_soft"])
	}
	if string(got.Settings["plan_tokens_hard"]) != "64000" {
		t.Errorf("plan_tokens_hard = %s, want 64000", got.Settings["plan_tokens_hard"])
	}
}

func TestSetProjectSettingsUnknownKeyRefused(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "proj", "Proj", "PRJ"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	err := s.SetProjectSettings(ctx, "proj", rawSettings(t, map[string]any{"bogus_key": 1}))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("SetProjectSettings: want ErrInvalidInput, got %v", err)
	}

	got, err := s.GetProject(ctx, "proj")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if len(got.Settings) != 0 {
		t.Errorf("Settings = %v, want empty (refused write left nothing)", got.Settings)
	}
}

func TestSetProjectSettingsWrongShapeRefused(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "proj", "Proj", "PRJ"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	for _, bad := range []map[string]any{
		{"plan_tokens_soft": "not a number"},
		{"plan_tokens_soft": 0},
		{"plan_tokens_soft": -5},
	} {
		err := s.SetProjectSettings(ctx, "proj", rawSettings(t, bad))
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("SetProjectSettings(%v): want ErrInvalidInput, got %v", bad, err)
		}
	}
}

func TestSetProjectSettingsNullRemovesKey(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "proj", "Proj", "PRJ"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.SetProjectSettings(ctx, "proj", rawSettings(t, map[string]any{
		"plan_tokens_soft": 32000,
		"plan_tokens_hard": 64000,
	})); err != nil {
		t.Fatalf("SetProjectSettings: %v", err)
	}

	if err := s.SetProjectSettings(ctx, "proj", map[string]json.RawMessage{
		"plan_tokens_soft": json.RawMessage("null"),
	}); err != nil {
		t.Fatalf("SetProjectSettings (remove): %v", err)
	}

	got, err := s.GetProject(ctx, "proj")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if _, ok := got.Settings["plan_tokens_soft"]; ok {
		t.Errorf("plan_tokens_soft still present: %v", got.Settings)
	}
	if string(got.Settings["plan_tokens_hard"]) != "64000" {
		t.Errorf("plan_tokens_hard = %s, want 64000 (untouched)", got.Settings["plan_tokens_hard"])
	}
}

func TestSetProjectSettingsNotFound(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()

	err := s.SetProjectSettings(ctx, "nope", rawSettings(t, map[string]any{"plan_tokens_soft": 1}))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetProjectSettings: want ErrNotFound, got %v", err)
	}
}
