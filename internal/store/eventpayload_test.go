package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// Every event the store records about a task names it under "task", so
// GET /api/v1/events attributes the event without a second read of
// state_log (025 §15.2). One test per event type, because each emit site
// builds its own payload and a shared assertion would let a new one ship
// unattributed.

// lastEventPayload returns the decoded payload of the newest event of typ,
// failing the test if there is none. Read straight from the table, without
// the commit horizon predicate: the caller has already committed the write,
// and a transaction always sees its own database's rows.
func lastEventPayload(t *testing.T, s *Store, typ string) map[string]any {
	t.Helper()
	var raw []byte
	if err := s.db.QueryRow(
		`SELECT payload FROM events WHERE type = $1 ORDER BY id DESC LIMIT 1`, typ,
	).Scan(&raw); err != nil {
		t.Fatalf("read %s event payload: %v", typ, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("%s payload %s is not a JSON object: %v", typ, raw, err)
	}
	return payload
}

// wantPayload asserts the newest event of typ carries exactly the given
// members.
func wantPayload(t *testing.T, s *Store, typ string, want map[string]any) {
	t.Helper()
	got := lastEventPayload(t, s, typ)
	if len(got) != len(want) {
		t.Fatalf("%s payload = %v, want %v", typ, got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s payload[%q] = %v, want %v (payload %v)", typ, k, got[k], v, got)
		}
	}
}

func TestClaimEventNamesTask(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.Claim(t.Context(), task.ID, "stig", "hel01:/wt/one", 0); err != nil {
		t.Fatalf("claim %s: %v", task.ID, err)
	}
	wantPayload(t, s, "lease.claimed", map[string]any{
		"task": task.ID, "actor": "stig", "worktree": "hel01:/wt/one",
	})
}

func TestRenewEventNamesTask(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	lease := leaseForTest(t, s, "hel01:/wt/one")

	if _, err := s.Renew(t.Context(), lease.TaskID, "stig", 0); err != nil {
		t.Fatalf("renew %s: %v", lease.TaskID, err)
	}
	wantPayload(t, s, "lease.renewed", map[string]any{"task": lease.TaskID, "actor": "stig"})
}

func TestReleaseEventNamesTask(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	lease := leaseForTest(t, s, "hel01:/wt/one")

	if err := s.Release(t.Context(), lease.TaskID, "stig"); err != nil {
		t.Fatalf("release %s: %v", lease.TaskID, err)
	}
	wantPayload(t, s, "lease.released", map[string]any{"task": lease.TaskID, "actor": "stig"})
}

func TestRebindLeaseEventNamesTask(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	lease := leaseForTest(t, s, "hel01:/wt/one")

	if _, err := s.RebindLeaseWorktree(t.Context(), lease.TaskID, "stig", "hel01:/wt/two"); err != nil {
		t.Fatalf("rebind %s: %v", lease.TaskID, err)
	}
	wantPayload(t, s, "lease.rebound", map[string]any{
		"task": lease.TaskID, "actor": "stig", "worktree": "hel01:/wt/two",
	})
}

func TestExpireLeaseEventNamesTask(t *testing.T) {
	t.Parallel()
	s, now := openLeaseStore(t)
	lease := leaseForTest(t, s, "hel01:/wt/one")

	*now = now.Add(DefaultLeaseTTL + time.Minute)
	if n, err := s.ExpireLeases(t.Context(), *now); err != nil || n != 1 {
		t.Fatalf("ExpireLeases = (%d, %v), want (1, nil)", n, err)
	}
	// The lease id rides along because the sweep is per lease, not per task:
	// a task can have had several, and the payload says which one expired.
	wantPayload(t, s, "lease.expired", map[string]any{
		"task": lease.TaskID, "lease": float64(lease.ID),
	})
}

func TestAgentSessionStartedEventNamesTask(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	lease := leaseForTest(t, s, "hel01:/wt/one")

	if _, err := s.TouchAgentSession(t.Context(), lease.TaskID, "stig",
		"claude-code", "2.0.0", "sess-1", nil); err != nil {
		t.Fatalf("touch agent session: %v", err)
	}
	wantPayload(t, s, "agent_session.started", map[string]any{
		"task": lease.TaskID, "actor": "stig", "agent": "claude-code", "session": "sess-1",
	})
}

func TestAgentSessionEndedEventNamesTask(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	lease := leaseForTest(t, s, "hel01:/wt/one")

	if _, err := s.TouchAgentSession(t.Context(), lease.TaskID, "stig",
		"claude-code", "2.0.0", "sess-1", nil); err != nil {
		t.Fatalf("touch agent session: %v", err)
	}
	if err := s.EndAgentSession(t.Context(), lease.TaskID, "stig",
		"claude-code", "sess-1", SessionUsage{}); err != nil {
		t.Fatalf("end agent session: %v", err)
	}
	wantPayload(t, s, "agent_session.ended", map[string]any{
		"task": lease.TaskID, "actor": "stig", "agent": "claude-code", "session": "sess-1",
	})
}

func TestEnqueueInstructionEventNamesTask(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.EnqueueInstruction(t.Context(), task.ID, "stig", "check the flaky test"); err != nil {
		t.Fatalf("enqueue instruction on %s: %v", task.ID, err)
	}
	wantPayload(t, s, "task.instructed", map[string]any{"task": task.ID, "actor": "stig"})
}

// TestAttributeEventToTask covers the seam the task-minting events use: the
// id does not exist when RecordEvent marshals the payload, so apply merges
// it into the row it just inserted, in the same transaction.
func TestAttributeEventToTask(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()

	t.Run("merges into an object payload", func(t *testing.T) {
		_, _, err := s.RecordEvent(ctx, "cli", "attr-object", "task.created",
			[]byte(`{"project":"horndb","title":"a task"}`),
			func(tx *sql.Tx, eventID int64) error {
				return AttributeEventToTask(tx, eventID, "HDB-1")
			})
		if err != nil {
			t.Fatalf("record event: %v", err)
		}
		wantPayload(t, s, "task.created", map[string]any{
			"project": "horndb", "title": "a task", "task": "HDB-1",
		})
	})

	t.Run("replaces a payload that is not an object", func(t *testing.T) {
		_, _, err := s.RecordEvent(ctx, "cli", "attr-null", "task.minted", nil,
			func(tx *sql.Tx, eventID int64) error {
				return AttributeEventToTask(tx, eventID, "HDB-2")
			})
		if err != nil {
			t.Fatalf("record event: %v", err)
		}
		wantPayload(t, s, "task.minted", map[string]any{"task": "HDB-2"})
	})

	t.Run("unknown event is ErrNotFound", func(t *testing.T) {
		err := s.Tx(ctx, func(tx *sql.Tx) error {
			return AttributeEventToTask(tx, 1<<40, "HDB-3")
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("AttributeEventToTask on a missing event = %v, want ErrNotFound", err)
		}
	})
}
