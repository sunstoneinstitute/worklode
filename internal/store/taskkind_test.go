package store

import (
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// A configured permission must govern every entry point that enforces it.
// Keep this test serial while it changes the process-wide registry.
func TestTaskKindPolicy(t *testing.T) {
	original := ns.TaskKindDescriptors["feature"]
	defer func() { ns.TaskKindDescriptors["feature"] = original }()
	for _, capability := range []string{"claim", "children", "decisions", "block", "close", "retag"} {
		t.Run(capability, func(t *testing.T) {
			s := openTaskStore(t)
			ns.TaskKindDescriptors["feature"] = original
			task := createTask(t, s, taskTestNow, defaultTaskInput())
			other := createTask(t, s, taskTestNow, defaultTaskInput())
			policy := original
			switch capability {
			case "claim":
				policy.Claimable = false
			case "children":
				policy.AllowsChildren = false
			case "decisions":
				policy.AllowsDecisions = false
			case "block":
				policy.CanBlock = false
			case "close":
				policy.ClosesOnAnswers = true
			case "retag":
				policy.KindMutable = false
			}
			ns.TaskKindDescriptors["feature"] = policy
			defer func() { ns.TaskKindDescriptors["feature"] = original }()
			switch capability {
			case "claim":
				candidates, err := s.readyCandidates(t.Context(), "horndb", "")
				if err != nil || len(candidates) != 0 {
					t.Fatalf("ready candidates = %v, %v; want none", candidates, err)
				}
				if _, err := s.Claim(t.Context(), task.ID, "stig", "host:/wt", 0); !errors.Is(err, ErrBadTransition) {
					t.Fatalf("claim: %v", err)
				}
			case "children":
				if err := addEdge(t, s, other.ID, task.ID, "child_of"); !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("child edge: %v", err)
				}
				if _, err := decompose(t, s, task.ID, []string{"child"}); !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("decompose: %v", err)
				}
			case "decisions":
				if _, err := s.AddDecision(t.Context(), task.ID, "stig", model.DecisionInput{Key: "q", Question: "Ship?", ResponseType: "yes_no"}); !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("pose: %v", err)
				}
			case "block":
				if err := addEdge(t, s, task.ID, other.ID, "blocks"); !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("block: %v", err)
				}
			case "close":
				poseOn(t, s, task.ID, "q", "yes_no")
				if _, err := s.RecordDecision(t.Context(), task.ID, "q", "stig", answerFor("yes_no")); err != nil {
					t.Fatal(err)
				}
				got, err := s.GetTask(t.Context(), task.ID)
				if err != nil || got.State != "merged" {
					t.Fatalf("answered task = %v, %v; want merged", got, err)
				}
			case "retag":
				if err := retag(t, s, task.ID, "chore"); !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("retag immutable kind: %v", err)
				}
			}
		})
	}
}

func TestDecisionKindRetag(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	for _, pair := range [][2]string{{"feature", "decision"}, {"decision", "feature"}, {"decision", "decision"}, {"feature", "typo"}} {
		in := defaultTaskInput()
		in.Kind = pair[0]
		task := createTask(t, s, taskTestNow, in)
		err := retag(t, s, task.ID, pair[1])
		if pair[0] == pair[1] {
			if err != nil {
				t.Fatalf("same kind: %v", err)
			}
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s -> %s: want ErrInvalidInput, got %v", pair[0], pair[1], err)
		}
		got, err := s.GetTask(t.Context(), task.ID)
		if err != nil || got.Kind != pair[0] {
			t.Fatalf("retag changed task: %v, %v", got, err)
		}
	}
}

func TestUnknownTaskKindPolicy(t *testing.T) {
	for _, kind := range []string{"", "typo", "spec"} {
		if _, err := taskKindPolicy(kind); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("policy %q: want ErrInvalidInput, got %v", kind, err)
		}
		if err := requireDecisionKind("task", kind); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("decisions on %q: want ErrInvalidInput, got %v", kind, err)
		}
	}
}
