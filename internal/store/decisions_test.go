package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestValidateDecisionSpec(t *testing.T) {
	opts := []model.DecisionOption{{Label: "a"}, {Label: "b"}, {Label: "c"}}
	two := 2
	one := 1
	zero := 0
	four := 4
	now := time.Now()
	decidedAt := &now
	cases := []struct {
		name    string
		spec    model.Decision
		wantErr bool
	}{
		{"single_select with options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select", Options: opts}, false},
		{"empty key is refused",
			model.Decision{Key: "", Question: "q", ResponseType: "single_select", Options: opts}, true},
		{"key with an uppercase letter is refused",
			model.Decision{Key: "Bad-Key", Question: "q", ResponseType: "single_select", Options: opts}, true},
		{"key with an underscore is refused",
			model.Decision{Key: "bad_key", Question: "q", ResponseType: "single_select", Options: opts}, true},
		{"empty question is refused",
			model.Decision{Key: "x", Question: "", ResponseType: "single_select", Options: opts}, true},
		{"unknown response_type is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "maybe", Options: opts}, true},

		{"multi_select with options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts}, false},
		{"multi_select without options is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select"}, true},

		{"single_select_notes with options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select_notes", Options: opts}, false},
		{"single_select_notes without options is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select_notes"}, true},

		{"pick_or_freetext with options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "pick_or_freetext", Options: opts}, false},
		{"pick_or_freetext without options is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "pick_or_freetext"}, true},

		{"yes_no without options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "yes_no"}, false},
		{"yes_no with options is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "yes_no", Options: opts}, true},

		{"freetext without options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "freetext"}, false},
		{"freetext with options is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "freetext", Options: opts}, true},

		{"duplicate option label is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select",
				Options: []model.DecisionOption{{Label: "a"}, {Label: "a"}}}, true},
		{"empty option label is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select",
				Options: []model.DecisionOption{{Label: "a"}, {Label: ""}}}, true},

		{"min_picks and max_picks on multi_select is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts,
				MinPicks: &one, MaxPicks: &two}, false},
		{"min_picks on single_select is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select", Options: opts,
				MinPicks: &one}, true},
		{"max_picks on single_select is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select", Options: opts,
				MaxPicks: &one}, true},
		{"min_picks below 1 is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts,
				MinPicks: &zero}, true},
		{"max_picks below 1 is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts,
				MaxPicks: &zero}, true},
		{"min_picks above max_picks is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts,
				MinPicks: &two, MaxPicks: &one}, true},
		{"max_picks above len(options) is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts,
				MaxPicks: &four}, true},

		{"posed answer is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "yes_no",
				Answer: &model.DecisionAnswer{Value: "yes"}}, true},
		{"posed decided_at is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "yes_no",
				DecidedAt: decidedAt}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDecisionSpec(tc.spec)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateDecisionSpec: %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error %v is not ErrInvalidInput", err)
			}
		})
	}
}

func TestValidateAnswer(t *testing.T) {
	opts := []model.DecisionOption{{Label: "a"}, {Label: "b"}, {Label: "c"}}
	two := 2
	cases := []struct {
		name    string
		spec    model.Decision
		answer  model.DecisionAnswer
		wantErr bool
	}{
		{"single_select picks one offered label",
			model.Decision{ResponseType: "single_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"b"}}, false},
		{"single_select refuses an unoffered label",
			model.Decision{ResponseType: "single_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"z"}}, true},
		{"single_select refuses smuggled notes",
			model.Decision{ResponseType: "single_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"a"}, Notes: "why not"}, true},
		{"single_select refuses more than one pick",
			model.Decision{ResponseType: "single_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"a", "b"}}, true},

		{"multi_select within max_picks",
			model.Decision{ResponseType: "multi_select", Options: opts, MaxPicks: &two},
			model.DecisionAnswer{Picked: []string{"a", "c"}}, false},
		{"multi_select refuses more than max_picks",
			model.Decision{ResponseType: "multi_select", Options: opts, MaxPicks: &two},
			model.DecisionAnswer{Picked: []string{"a", "b", "c"}}, true},
		{"multi_select refuses a repeated pick",
			model.Decision{ResponseType: "multi_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"a", "a"}}, true},
		{"multi_select refuses an empty pick under the default minimum",
			model.Decision{ResponseType: "multi_select", Options: opts},
			model.DecisionAnswer{Picked: []string{}}, true},

		{"single_select_notes picks one and gives notes",
			model.Decision{ResponseType: "single_select_notes", Options: opts},
			model.DecisionAnswer{Picked: []string{"a"}, Notes: "because"}, false},
		{"single_select_notes refuses empty notes",
			model.Decision{ResponseType: "single_select_notes", Options: opts},
			model.DecisionAnswer{Picked: []string{"a"}}, true},

		{"pick_or_freetext accepts a pick",
			model.Decision{ResponseType: "pick_or_freetext", Options: opts},
			model.DecisionAnswer{Picked: []string{"a"}}, false},
		{"pick_or_freetext accepts freetext",
			model.Decision{ResponseType: "pick_or_freetext", Options: opts},
			model.DecisionAnswer{Freetext: "something else"}, false},
		{"pick_or_freetext refuses both a pick and freetext",
			model.Decision{ResponseType: "pick_or_freetext", Options: opts},
			model.DecisionAnswer{Picked: []string{"a"}, Freetext: "something else"}, true},
		{"pick_or_freetext refuses neither a pick nor freetext",
			model.Decision{ResponseType: "pick_or_freetext", Options: opts},
			model.DecisionAnswer{}, true},

		{"yes_no takes the third value",
			model.Decision{ResponseType: "yes_no"},
			model.DecisionAnswer{Value: "unsure"}, false},
		{"yes_no refuses smuggled freetext",
			model.Decision{ResponseType: "yes_no"},
			model.DecisionAnswer{Value: "yes", Freetext: "but"}, true},
		{"yes_no refuses a value outside yes/no/unsure",
			model.Decision{ResponseType: "yes_no"},
			model.DecisionAnswer{Value: "maybe"}, true},

		{"freetext accepts non-empty text",
			model.Decision{ResponseType: "freetext"},
			model.DecisionAnswer{Freetext: "here is my answer"}, false},
		{"freetext refuses empty text",
			model.Decision{ResponseType: "freetext"},
			model.DecisionAnswer{}, true},
		{"freetext refuses smuggled picked",
			model.Decision{ResponseType: "freetext"},
			model.DecisionAnswer{Freetext: "here", Picked: []string{"a"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAnswer(tc.spec, tc.answer)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateAnswer: %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error %v is not ErrInvalidInput", err)
			}
		})
	}
}

// answerFor is the smallest legal answer for each response type, so a test
// about the record-answer mutation does not restate §10.1's answer rules.
func answerFor(responseType string) model.DecisionAnswer {
	switch responseType {
	case "single_select", "pick_or_freetext":
		return model.DecisionAnswer{Picked: []string{"a"}}
	case "multi_select":
		return model.DecisionAnswer{Picked: []string{"a", "b"}}
	case "single_select_notes":
		return model.DecisionAnswer{Picked: []string{"a"}, Notes: "because"}
	case "yes_no":
		return model.DecisionAnswer{Value: "yes"}
	default:
		return model.DecisionAnswer{Freetext: "in my own words"}
	}
}

// poseOn poses one question of the given response type on taskID.
func poseOn(t *testing.T, s *Store, taskID, key, responseType string) {
	t.Helper()
	in := model.DecisionInput{Key: key, Question: "Which way?", ResponseType: responseType}
	switch responseType {
	case "single_select", "multi_select", "single_select_notes", "pick_or_freetext":
		in.Options = []model.DecisionOption{{Label: "a"}, {Label: "b"}}
	}
	if _, err := s.AddDecision(t.Context(), taskID, "stig", in); err != nil {
		t.Fatalf("pose %s/%s: %v", taskID, key, err)
	}
}

// decisionTask creates a decision-kind task in "ready".
func decisionTask(t *testing.T, s *Store) *model.Task {
	t.Helper()
	in := defaultTaskInput()
	in.Kind = "decision"
	return createTask(t, s, taskTestNow, in)
}

// mergedOnDecidedEvent counts the state_log rows moving taskID to merged that
// hang off a task.decided event. One means the answer and the closure rode the
// same transaction; zero means two transactions happened.
func mergedOnDecidedEvent(t *testing.T, s *Store, taskID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT count(*) FROM state_log sl JOIN events e ON e.id = sl.event_id
		  WHERE sl.entity_kind = 'task' AND sl.entity_id = $1
		    AND sl.change->>'field' = 'state' AND sl.change->>'new' = 'merged'
		    AND e.type = 'task.decided'`, taskID).Scan(&n); err != nil {
		t.Fatalf("count merged transitions on task.decided: %v", err)
	}
	return n
}

// TestRecordDecisionClosesTheTask is the one-transaction proof: the answer,
// the auto-assignment and the move to merged all hang off one task.decided
// event.
func TestRecordDecisionClosesTheTask(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := decisionTask(t, s)
	poseOn(t, s, task.ID, "q1", "single_select")

	d, err := s.RecordDecision(t.Context(), task.ID, "q1", "stig", answerFor("single_select"))
	if err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if d.Answer == nil || len(d.Answer.Picked) != 1 || d.Answer.Picked[0] != "a" {
		t.Fatalf("answer not echoed: %+v", d)
	}
	if d.DecidedAt == nil || d.DecidedBy != "stig" {
		t.Fatalf("decided_by/decided_at not recorded: %+v", d)
	}
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != "merged" {
		t.Fatalf("state = %q, want merged: the record and the closure are one act", got.State)
	}
	if got.Assignee != "stig" {
		t.Fatalf("assignee = %q, want stig: an unanswered task falls to the decider", got.Assignee)
	}
	if n := mergedOnDecidedEvent(t, s, task.ID); n != 1 {
		t.Fatalf("merged transitions on task.decided = %d, want 1", n)
	}
}

// TestRecordDecisionEveryResponseType: each of §10.1's six types records its
// own answer shape and closes its one-row decision task.
func TestRecordDecisionEveryResponseType(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	for _, rt := range decisionResponseTypes {
		t.Run(rt, func(t *testing.T) {
			task := decisionTask(t, s)
			poseOn(t, s, task.ID, "q1", rt)
			d, err := s.RecordDecision(t.Context(), task.ID, "q1", "stig", answerFor(rt))
			if err != nil {
				t.Fatalf("RecordDecision(%s): %v", rt, err)
			}
			if d.Answer == nil || d.DecidedAt == nil || d.DecidedBy != "stig" {
				t.Fatalf("%s: answer/decided_by/decided_at not recorded: %+v", rt, d)
			}
			mustState(t, s, task.ID, "merged")
		})
	}
}

// TestRecordDecisionClosesOnTheLastRow: a decision task with two questions
// stays open until both are answered.
func TestRecordDecisionClosesOnTheLastRow(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := decisionTask(t, s)
	poseOn(t, s, task.ID, "q1", "yes_no")
	poseOn(t, s, task.ID, "q2", "yes_no")

	if _, err := s.RecordDecision(t.Context(), task.ID, "q1", "stig", answerFor("yes_no")); err != nil {
		t.Fatalf("answer q1: %v", err)
	}
	mustState(t, s, task.ID, "ready")

	if _, err := s.RecordDecision(t.Context(), task.ID, "q2", "stig", answerFor("yes_no")); err != nil {
		t.Fatalf("answer q2: %v", err)
	}
	mustState(t, s, task.ID, "merged")
}

// TestRecordDecisionLeavesOtherKindsOpen: only a decision-kind task closes on
// its last answer. A feature task carrying a question keeps its own state.
func TestRecordDecisionLeavesOtherKindsOpen(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	poseOn(t, s, task.ID, "q1", "yes_no")

	if _, err := s.RecordDecision(t.Context(), task.ID, "q1", "stig", answerFor("yes_no")); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	mustState(t, s, task.ID, "ready")
	if n := mergedOnDecidedEvent(t, s, task.ID); n != 0 {
		t.Fatalf("feature task moved to merged on an answer (%d transitions)", n)
	}
}

// TestRecordDecisionRefusals covers every refusal, each asserting the row and
// the task are left exactly as they were.
func TestRecordDecisionRefusals(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()
	if err := s.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	seedParticipant(t, s, "horndb", "bob", "member", false)

	t.Run("an answered row is never written over", func(t *testing.T) {
		// A feature task, so the first answer does not close the task and the
		// refusal is about the row rather than the state.
		task := createTask(t, s, taskTestNow, defaultTaskInput())
		poseOn(t, s, task.ID, "q1", "yes_no")
		if _, err := s.RecordDecision(ctx, task.ID, "q1", "stig", answerFor("yes_no")); err != nil {
			t.Fatalf("first answer: %v", err)
		}
		_, err := s.RecordDecision(ctx, task.ID, "q1", "stig", model.DecisionAnswer{Value: "no"})
		if !errors.Is(err, ErrBadTransition) {
			t.Fatalf("re-answer err = %v, want ErrBadTransition", err)
		}
		d, err := s.GetDecision(ctx, task.ID, "q1")
		if err != nil || d.Answer == nil || d.Answer.Value != "yes" {
			t.Fatalf("row after a refused re-answer = %+v (%v), want the first answer", d, err)
		}
	})

	t.Run("a task assigned to someone else is theirs to decide", func(t *testing.T) {
		task := decisionTask(t, s)
		poseOn(t, s, task.ID, "q1", "yes_no")
		if _, _, err := s.RecordEvent(ctx, "cli", nextExt(t), "task.assign", nil,
			func(tx *sql.Tx, eventID int64) error {
				return AssignTask(tx, taskTestNow, task.ID, "bob", eventID)
			}); err != nil {
			t.Fatalf("assign to bob: %v", err)
		}
		err := recordAndExpectRefusal(t, s, task.ID, "q1", "stig", ErrInvalidInput)
		if !strings.Contains(err.Error(), "bob") {
			t.Fatalf("refusal %v does not name the assignee", err)
		}
	})

	t.Run("a blocked task is ordered behind work that has not closed", func(t *testing.T) {
		task := decisionTask(t, s)
		poseOn(t, s, task.ID, "q1", "yes_no")
		blocker := createTask(t, s, taskTestNow, defaultTaskInput())
		if err := addEdge(t, s, blocker.ID, task.ID, "blocks"); err != nil {
			t.Fatalf("add blocking edge: %v", err)
		}
		recordAndExpectRefusal(t, s, task.ID, "q1", "stig", ErrInvalidInput)
	})

	t.Run("an abandoned task has nothing left to decide", func(t *testing.T) {
		task := decisionTask(t, s)
		poseOn(t, s, task.ID, "q1", "yes_no")
		walkTo(t, s, task.ID, "abandoned")
		recordAndExpectRefusal(t, s, task.ID, "q1", "stig", ErrBadTransition)
	})

	t.Run("an answer the response type does not define", func(t *testing.T) {
		task := decisionTask(t, s)
		poseOn(t, s, task.ID, "q1", "yes_no")
		if _, err := s.RecordDecision(ctx, task.ID, "q1", "stig",
			model.DecisionAnswer{Value: "maybe"}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid value err = %v, want ErrInvalidInput", err)
		}
		d, err := s.GetDecision(ctx, task.ID, "q1")
		if err != nil || d.Answer != nil {
			t.Fatalf("a refused answer wrote to the row: %+v (%v)", d, err)
		}
		mustState(t, s, task.ID, "ready")
	})

	t.Run("an unknown key", func(t *testing.T) {
		task := decisionTask(t, s)
		poseOn(t, s, task.ID, "q1", "yes_no")
		if _, err := s.RecordDecision(ctx, task.ID, "nope", "stig",
			answerFor("yes_no")); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown key err = %v, want ErrNotFound", err)
		}
	})
}

// recordAndExpectRefusal answers a question expecting want, and asserts the
// row is still unanswered and the task's state is unmoved.
func recordAndExpectRefusal(t *testing.T, s *Store, taskID, key, actor string, want error) error {
	t.Helper()
	before, err := s.GetTask(t.Context(), taskID)
	if err != nil {
		t.Fatalf("GetTask before: %v", err)
	}
	_, err = s.RecordDecision(t.Context(), taskID, key, actor, answerFor("yes_no"))
	if !errors.Is(err, want) {
		t.Fatalf("RecordDecision err = %v, want %v", err, want)
	}
	d, gerr := s.GetDecision(t.Context(), taskID, key)
	if gerr != nil {
		t.Fatalf("GetDecision after: %v", gerr)
	}
	if d.Answer != nil || d.DecidedAt != nil || d.DecidedBy != "" {
		t.Fatalf("a refused answer wrote to the row: %+v", d)
	}
	after, gerr := s.GetTask(t.Context(), taskID)
	if gerr != nil {
		t.Fatalf("GetTask after: %v", gerr)
	}
	if after.State != before.State {
		t.Fatalf("a refused answer moved the task %s -> %s", before.State, after.State)
	}
	return err
}
