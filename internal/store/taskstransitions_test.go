package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTransitionLegal(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	cases := []struct{ from, to string }{
		{"draft", "ready"},
		{"ready", "in_progress"},
		{"in_progress", "in_review"},
		{"in_progress", "ready"},
		{"in_review", "in_progress"},
		{"ready", "merged"},
		{"in_progress", "merged"},
		{"in_review", "merged"},
		{"merged", "deployed_dev"},
		{"merged", "deployed_prod"},
		{"merged", "released"},
		{"deployed_dev", "deployed_prod"},
		{"deployed_dev", "released"},
		{"draft", "abandoned"},
		{"ready", "abandoned"},
		{"in_progress", "abandoned"},
		{"in_review", "abandoned"},
		{"merged", "ready"},
		{"deployed_dev", "ready"},
		{"deployed_prod", "ready"},
		{"released", "ready"},
		{"abandoned", "ready"},
	}
	for _, c := range cases {
		in := defaultTaskInput()
		in.Draft = c.from == "draft"
		task := createTask(t, s, taskTestNow, in)
		if !in.Draft {
			walkTo(t, s, task.ID, c.from)
		}
		if err := transition(t, s, taskTestNow, task.ID, c.from, c.to); err != nil {
			t.Fatalf("transition %s -> %s: %v", c.from, c.to, err)
		}
		got, err := s.GetTask(t.Context(), task.ID)
		if err != nil {
			t.Fatalf("GetTask %s: %v", task.ID, err)
		}
		if got.State != c.to {
			t.Fatalf("after %s -> %s: state is %q", c.from, c.to, got.State)
		}
	}
}

func TestTransitionIllegal(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	cases := []struct{ from, to string }{
		{"draft", "merged"},
		{"draft", "in_progress"},
		{"merged", "abandoned"},
		{"released", "deployed_dev"},
		{"abandoned", "merged"},
		{"abandoned", "in_progress"},
	}
	for _, c := range cases {
		in := defaultTaskInput()
		in.Draft = c.from == "draft"
		task := createTask(t, s, taskTestNow, in)
		if !in.Draft {
			walkTo(t, s, task.ID, c.from)
		}
		err := transition(t, s, taskTestNow, task.ID, c.from, c.to)
		if !errors.Is(err, ErrBadTransition) {
			t.Fatalf("transition %s -> %s: want ErrBadTransition, got %v", c.from, c.to, err)
		}
	}
}

func TestTransitionWrongCurrentState(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput()) // state: ready
	err := transition(t, s, taskTestNow, task.ID, "in_progress", "in_review")
	if !errors.Is(err, ErrBadTransition) {
		t.Fatalf("transition with wrong from: want ErrBadTransition, got %v", err)
	}
	// The task is untouched.
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != "ready" {
		t.Fatalf("state after failed transition: got %q, want ready", got.State)
	}
}

func TestTransitionUnknownTask(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	err := transition(t, s, taskTestNow, "HDB-999", "ready", "in_progress")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("transition unknown task: want ErrNotFound, got %v", err)
	}
}

func TestTransitionWritesStateLogAndBumpsUpdatedAt(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	created := taskTestNow
	moved := taskTestNow.Add(5 * time.Minute)

	task := createTask(t, s, created, defaultTaskInput())

	var eventID int64
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.transition", nil,
		func(tx *sql.Tx, evID int64) error {
			eventID = evID
			return Transition(tx, moved, task.ID, "ready", "in_progress", evID)
		})
	if err != nil {
		t.Fatalf("transition: %v", err)
	}

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt: got %v, want %v", got.CreatedAt, created)
	}
	if !got.UpdatedAt.Equal(moved) {
		t.Fatalf("UpdatedAt: got %v, want %v (bumped)", got.UpdatedAt, moved)
	}

	// entity_id alone no longer picks one row: CreateTask now logs its own
	// state_log entry too, so this reads the transition's row specifically,
	// by the eventID RecordEvent handed the apply callback above.
	var kind, entityID, changeJSON string
	var loggedEventID int64
	row := s.db.QueryRow(
		`SELECT entity_kind, entity_id, change, event_id FROM state_log WHERE entity_id = $1 AND event_id = $2`,
		task.ID, eventID)
	if err := row.Scan(&kind, &entityID, &changeJSON, &loggedEventID); err != nil {
		t.Fatalf("read state_log: %v", err)
	}
	if kind != "task" || entityID != task.ID || loggedEventID != eventID {
		t.Fatalf("state_log row: kind=%q entity=%q event_id=%d, want task/%s/%d",
			kind, entityID, loggedEventID, task.ID, eventID)
	}
	var change map[string]string
	if err := json.Unmarshal([]byte(changeJSON), &change); err != nil {
		t.Fatalf("unmarshal change %q: %v", changeJSON, err)
	}
	want := map[string]string{"field": "state", "old": "ready", "new": "in_progress"}
	if !reflect.DeepEqual(change, want) {
		t.Fatalf("state_log change: got %v, want %v", change, want)
	}
}

// TestDeliveredStateSetCoversDeliveryRanks pins deliveredStateSet against the
// delivery axis it is derived from: every ranked state plus abandoned, and
// nothing else. A state added to deliveryRanks without reaching the set would
// leave assign.go's state-only guards and taskClosed disagreeing on what
// "delivered" means.
func TestDeliveredStateSetCoversDeliveryRanks(t *testing.T) {
	t.Parallel()
	want := append(slices.Sorted(maps.Keys(deliveryRanks)), "abandoned")
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(deliveredStateSet)); !slices.Equal(got, want) {
		t.Fatalf("deliveredStateSet = %v, want %v", got, want)
	}
}

// TestDeliveryRanksMatchLegalTransitions pins the reason the two terminals
// share a rank: a state ranked *below* another it cannot legally transition to
// is a wedge — taskClosed would hold a task short of some repo's done_state
// with no move left to make. Every ranked state must therefore be able to
// reach any strictly higher-ranked one.
func TestDeliveryRanksMatchLegalTransitions(t *testing.T) {
	t.Parallel()
	for from, fromRank := range deliveryRanks {
		for to, toRank := range deliveryRanks {
			if toRank <= fromRank {
				continue
			}
			if !legalTransitions[[2]string{from, to}] {
				t.Errorf("%s ranks %d, below %s at %d, but %s -> %s is not a legal transition: "+
					"a task at %s in a repo gating on %s could never close",
					from, fromRank, to, toRank, from, to, from, to)
			}
		}
	}
}

// TestTaskStateShapeMatchesStateMachine pins ns/shapes.ttl's wl:taskState
// sh:in list to the states legalTransitions can reach. The shape is
// hand-written Turtle with no generator behind it — the state machine is Go,
// not an enum in ns/concept.ttl — so this test is what keeps the duplicate
// honest. docs/follow-ups.md flagged exactly this drift.
func TestTaskStateShapeMatchesStateMachine(t *testing.T) {
	t.Parallel()
	shapes, err := os.ReadFile(filepath.Join("..", "..", "ns", "shapes.ttl"))
	if err != nil {
		t.Fatalf("read ns/shapes.ttl: %v", err)
	}

	// The sh:in list on the property shape whose sh:path is wl:taskState.
	// [^]]*? keeps the match inside that one property shape's brackets.
	re := regexp.MustCompile(`sh:path wl:taskState ;[^\]]*?sh:in \(([^)]*)\)`)
	m := re.FindSubmatch(shapes)
	if m == nil {
		t.Fatal("no `sh:path wl:taskState` property shape with an sh:in list in ns/shapes.ttl")
	}
	inShape := strings.FieldsFunc(string(m[1]), func(r rune) bool {
		return r == '"' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})
	slices.Sort(inShape)

	if want := allStates(); !slices.Equal(inShape, want) {
		t.Errorf("wl:taskState sh:in = %v, want %v\n"+
			"ns/shapes.ttl and legalTransitions disagree; widen both together",
			inShape, want)
	}
}
