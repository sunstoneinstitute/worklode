package store

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// insertActivity appends one activity row for taskID and fails the test on
// error, returning the number of rows the call actually inserted (0 for an
// unknown task).
func insertActivity(t *testing.T, s *Store, taskID string, at time.Time, event string, attrs model.ActivityAttrs) int {
	t.Helper()
	n, err := s.AppendTaskActivity(t.Context(), []model.TaskActivity{
		{Task: taskID, Actor: "stig", Agent: "claude-code", Session: "sess-1", At: at, Event: event, Attrs: attrs},
	})
	if err != nil {
		t.Fatalf("append task activity: %v", err)
	}
	return n
}

func TestAppendTaskActivity(t *testing.T) {
	t.Parallel()

	t.Run("empty slice inserts nothing and issues no query", func(t *testing.T) {
		s, _ := openLeaseStore(t)
		n, err := s.AppendTaskActivity(t.Context(), nil)
		if err != nil {
			t.Fatalf("append nil: %v", err)
		}
		if n != 0 {
			t.Fatalf("append nil: got %d, want 0", n)
		}
	})

	t.Run("inserts known task rows, skips unknown task, attrs round-trip", func(t *testing.T) {
		s, _ := openLeaseStore(t)
		task := createTask(t, s, leaseTestNow, defaultTaskInput())

		success := false
		rows := []model.TaskActivity{
			{
				Task: task.ID, Actor: "stig", Agent: "claude-code", Session: "sess-1",
				At: leaseTestNow, Event: "claude_code.tool_result",
				Attrs: model.ActivityAttrs{
					ToolName: "Bash", Success: &success, DurationMS: 120,
					ErrorType: "timeout", ToolUseID: "tu-1",
				},
			},
			{
				Task: "WL-DOES-NOT-EXIST", Actor: "stig", Agent: "claude-code", Session: "sess-1",
				At: leaseTestNow, Event: "claude_code.tool_result",
			},
		}

		n, err := s.AppendTaskActivity(t.Context(), rows)
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if n != 1 {
			t.Fatalf("append: got %d inserted, want 1", n)
		}

		got, err := s.TaskActivity(t.Context(), task.ID, 0, 10)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("read back: got %d rows, want 1", len(got))
		}
		row := got[0]
		if row.Task != task.ID || row.Event != "claude_code.tool_result" {
			t.Fatalf("read back row: got %+v", row)
		}
		if row.Attrs.ToolName != "Bash" || row.Attrs.DurationMS != 120 ||
			row.Attrs.ErrorType != "timeout" || row.Attrs.ToolUseID != "tu-1" {
			t.Fatalf("attrs round-trip: got %+v", row.Attrs)
		}
		if row.Attrs.Success == nil || *row.Attrs.Success != false {
			t.Fatalf("attrs.Success round-trip: got %v, want pointer to false", row.Attrs.Success)
		}
	})

	t.Run("ids follow the batch order", func(t *testing.T) {
		s, _ := openLeaseStore(t)
		task := createTask(t, s, leaseTestNow, defaultTaskInput())

		// One export arrives as one batch, and the task page reads it back
		// by id. Without an explicit order on the insert, ids may be handed
		// out in any order within the batch, which would show a tool result
		// before the decision that produced it.
		rows := make([]model.TaskActivity, 8)
		for i := range rows {
			rows[i] = model.TaskActivity{
				Task: task.ID, Actor: "stig", Agent: "claude-code", Session: "sess-1",
				At: leaseTestNow, Event: fmt.Sprintf("e%d", i),
			}
		}
		if _, err := s.AppendTaskActivity(t.Context(), rows); err != nil {
			t.Fatalf("append: %v", err)
		}

		got, err := s.TaskActivity(t.Context(), task.ID, 0, len(rows))
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		// Newest first, so reading the page backwards is the input order.
		slices.Reverse(got)
		for i, a := range got {
			if a.Event != rows[i].Event {
				t.Fatalf("row %d in id order is %q, want %q (ids: %v)", i, a.Event, rows[i].Event, events(got))
			}
		}
	})
}

func TestTaskActivityCursor(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	// Inserted one at a time so each row gets its own timestamp as well as
	// its own id; the batch path's own ordering is pinned above.
	for i := 0; i < 5; i++ {
		insertActivity(t, s, task.ID, leaseTestNow.Add(time.Duration(i)*time.Second), fmt.Sprintf("e%d", i), model.ActivityAttrs{})
	}

	// after == 0: newest 3, descending by id.
	page, err := s.TaskActivity(t.Context(), task.ID, 0, 3)
	if err != nil {
		t.Fatalf("newest page: %v", err)
	}
	if got := events(page); !equalSlices(got, []string{"e4", "e3", "e2"}) {
		t.Fatalf("newest page: got %v, want [e4 e3 e2]", got)
	}

	// Find e1's id to use as a cursor.
	all, err := s.TaskActivity(t.Context(), task.ID, 0, 10)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	var afterID int64
	for _, a := range all {
		if a.Event == "e1" {
			afterID = a.ID
		}
	}
	if afterID == 0 {
		t.Fatalf("did not find e1's id")
	}

	// after > 0: rows with id > after, ascending, up to limit.
	tail, err := s.TaskActivity(t.Context(), task.ID, afterID, 10)
	if err != nil {
		t.Fatalf("tail page: %v", err)
	}
	if got := events(tail); !equalSlices(got, []string{"e2", "e3", "e4"}) {
		t.Fatalf("tail page: got %v, want [e2 e3 e4]", got)
	}

	// limit is honoured on the ascending page too.
	tailLimited, err := s.TaskActivity(t.Context(), task.ID, afterID, 1)
	if err != nil {
		t.Fatalf("tail limited page: %v", err)
	}
	if got := events(tailLimited); !equalSlices(got, []string{"e2"}) {
		t.Fatalf("tail limited page: got %v, want [e2]", got)
	}
}

func events(rows []model.TaskActivity) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Event
	}
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPurgeTaskActivity(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)

	closedTask := createTask(t, s, leaseTestNow, defaultTaskInput())
	walkTo(t, s, closedTask.ID, "merged") // no commits landed, so taskClosed(t) is true

	openTask := createTask(t, s, leaseTestNow, defaultTaskInput())
	// openTask stays "ready" -- taskClosed(t) is false.

	insertActivity(t, s, closedTask.ID, leaseTestNow.Add(-3*time.Hour), "old-closed", model.ActivityAttrs{})
	insertActivity(t, s, closedTask.ID, leaseTestNow.Add(-1*time.Hour), "fresh-closed", model.ActivityAttrs{})
	insertActivity(t, s, openTask.ID, leaseTestNow.Add(-8*24*time.Hour), "old-open", model.ActivityAttrs{})
	insertActivity(t, s, openTask.ID, leaseTestNow.Add(-3*time.Hour), "mid-open", model.ActivityAttrs{})
	insertActivity(t, s, openTask.ID, leaseTestNow.Add(-10*time.Minute), "fresh-open", model.ActivityAttrs{})

	n, err := s.PurgeTaskActivity(t.Context(), leaseTestNow)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 2 {
		t.Fatalf("purge: got %d deleted, want 2", n)
	}

	closedRows, err := s.TaskActivity(t.Context(), closedTask.ID, 0, 10)
	if err != nil {
		t.Fatalf("read closed task rows: %v", err)
	}
	if got := events(closedRows); !equalSlices(got, []string{"fresh-closed"}) {
		t.Fatalf("closed task rows after purge: got %v, want [fresh-closed]", got)
	}

	openRows, err := s.TaskActivity(t.Context(), openTask.ID, 0, 10)
	if err != nil {
		t.Fatalf("read open task rows: %v", err)
	}
	// order is newest-first (descending by id, which matches insertion order here)
	if got := events(openRows); !equalSlices(got, []string{"fresh-open", "mid-open"}) {
		t.Fatalf("open task rows after purge: got %v, want [fresh-open mid-open]", got)
	}
}
