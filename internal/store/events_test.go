package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRecordEventIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	applyCalls := 0
	apply := func(tx *sql.Tx, eventID int64) error {
		applyCalls++
		return nil
	}

	id1, inserted1, err := s.RecordEvent(ctx, "github", "d1", "push", nil, apply)
	if err != nil {
		t.Fatalf("first RecordEvent: %v", err)
	}
	if !inserted1 {
		t.Fatalf("first RecordEvent: want inserted=true, got false")
	}
	if applyCalls != 1 {
		t.Fatalf("first RecordEvent: want apply called once, got %d", applyCalls)
	}

	id2, inserted2, err := s.RecordEvent(ctx, "github", "d1", "push", nil, apply)
	if err != nil {
		t.Fatalf("second RecordEvent: %v", err)
	}
	if inserted2 {
		t.Fatalf("second RecordEvent: want inserted=false, got true")
	}
	if id2 != id1 {
		t.Fatalf("second RecordEvent: want same id %d, got %d", id1, id2)
	}
	if applyCalls != 1 {
		t.Fatalf("second RecordEvent: apply must not be called on replay, but was called %d times total", applyCalls)
	}
}

func TestRecordEventAppliesStateInSameTx(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	boom := errors.New("apply failed")
	apply := func(tx *sql.Tx, eventID int64) error {
		return boom
	}

	_, _, err := s.RecordEvent(ctx, "github", "d2", "push", nil, apply)
	if err == nil {
		t.Fatalf("RecordEvent: want error from apply, got nil")
	}

	var count int
	row := s.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE source = ? AND external_id = ?`, "github", "d2")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("query events: %v", err)
	}
	if count != 0 {
		t.Fatalf("want event row rolled back, but found %d rows", count)
	}
}

func TestLogChange(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	change := map[string]any{"field": "state", "old": "ready", "new": "in_progress"}
	apply := func(tx *sql.Tx, eventID int64) error {
		return LogChange(tx, "task", "WT-1", eventID, change)
	}

	eventID, inserted, err := s.RecordEvent(ctx, "cli", "c1", "transition", nil, apply)
	if err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	if !inserted {
		t.Fatalf("RecordEvent: want inserted=true")
	}

	var entityKind, entityID, changeJSON string
	var gotEventID int64
	row := s.db.QueryRowContext(ctx, `SELECT entity_kind, entity_id, change, event_id FROM state_log`)
	if err := row.Scan(&entityKind, &entityID, &changeJSON, &gotEventID); err != nil {
		t.Fatalf("query state_log: %v", err)
	}

	if entityKind != "task" || entityID != "WT-1" {
		t.Fatalf("state_log entity: got (%q, %q)", entityKind, entityID)
	}
	if gotEventID != eventID {
		t.Fatalf("state_log event_id: want %d, got %d", eventID, gotEventID)
	}

	var gotChange map[string]any
	if err := json.Unmarshal([]byte(changeJSON), &gotChange); err != nil {
		t.Fatalf("unmarshal change: %v", err)
	}
	if gotChange["field"] != "state" || gotChange["old"] != "ready" || gotChange["new"] != "in_progress" {
		t.Fatalf("state_log change: got %v", gotChange)
	}
}
