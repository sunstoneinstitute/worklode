package api

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestSelectMode(t *testing.T) {
	tests := []struct {
		name string
		in   modeFacts
		want cockpitMode
	}{
		{"candidate", modeFacts{IntakeCandidate: true}, modeEditorialDecision},
		{"promoted launch", modeFacts{PromotedFromIntake: true}, modeApprovedLaunch},
		{"entered research", modeFacts{PromotedFromIntake: true, EnteredResearch: true}, modeOperations},
		{"ordinary project", modeFacts{}, modeOperations},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectMode(tt.in); got != tt.want {
				t.Fatalf("selectMode(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestStateEvidence exhaustively covers stateEvidence's classification: no
// event is always declared; github/flux/watcher/system are always observed
// regardless of event type; a cli-sourced lease.* event is observed (the
// lease machinery enforces it); every other cli-sourced event is
// user-reported (a human or agent typed a command).
func TestStateEvidence(t *testing.T) {
	tests := []struct {
		source, eventType string
		hasEvent          bool
		want              evidenceCategory
	}{
		{"", "", false, evidenceDeclared},
		{"github", "pull_request", true, evidenceObserved},
		{"flux", "kustomization.applied", true, evidenceObserved},
		{"watcher", "pod.crashloop", true, evidenceObserved},
		{"system", "lease.expired", true, evidenceObserved},
		{"cli", "lease.claimed", true, evidenceObserved},
		{"cli", "task.started", true, evidenceUserReported},
		{"cli", "task.stopped", true, evidenceUserReported},
		{"cli", "task.updated", true, evidenceUserReported},
	}
	for _, tt := range tests {
		if got := stateEvidence(tt.source, tt.eventType, tt.hasEvent); got != tt.want {
			t.Errorf("stateEvidence(%q, %q, %v) = %q, want %q", tt.source, tt.eventType, tt.hasEvent, got, tt.want)
		}
	}
}

// TestEvidenceCategoryLabel pins every evidenceCategory's display text
// exactly, so a future refactor cannot silently start deriving it by
// replacing underscores with spaces (which would render "user_reported" as
// "User reported" instead of "User-reported").
func TestEvidenceCategoryLabel(t *testing.T) {
	tests := []struct {
		cat  evidenceCategory
		want string
	}{
		{evidenceDeclared, "Declared"},
		{evidenceUserReported, "User-reported"},
		{evidenceObserved, "Observed"},
		{evidenceRecommended, "Recommended"},
	}
	for _, tt := range tests {
		if got := tt.cat.Label(); got != tt.want {
			t.Errorf("%q.Label() = %q, want %q", tt.cat, got, tt.want)
		}
	}
}

// stubActors returns a resolveActor func backed by a fixed in-memory set, so
// mapWorkItem's owner/delegate logic can be tested without a store.
func stubActors(actors map[string]*store.Actor) func(string) (*store.Actor, error) {
	return func(id string) (*store.Actor, error) {
		if id == "" {
			return nil, nil
		}
		return actors[id], nil
	}
}

// TestMapWorkItemOwnerAndDelegate asserts a human assignee becomes owner and
// an unreleased agent lease becomes delegate — the two are resolved from
// entirely different facts (Task.Assignee vs. an unreleased lease) and must
// never collapse into each other.
func TestMapWorkItemOwnerAndDelegate(t *testing.T) {
	resolve := stubActors(map[string]*store.Actor{
		"dana":      {ID: "dana", Kind: "human", DisplayName: "Dana"},
		"agent-one": {ID: "agent-one", Kind: "agent", DisplayName: "Agent One"},
	})
	fact := store.ProjectWorkFact{
		Task:  store.Task{ID: "WL-1", Title: "Ship it", State: "in_progress", Assignee: "dana"},
		Lease: &store.Lease{ActorID: "agent-one"},
	}

	item, err := mapWorkItem(fact, false, resolve)
	if err != nil {
		t.Fatalf("mapWorkItem: %v", err)
	}
	if item.Owner == nil || item.Owner.ID != "dana" || item.Owner.Name != "Dana" {
		t.Errorf("owner = %#v, want dana/Dana", item.Owner)
	}
	if item.Delegate == nil || item.Delegate.ID != "agent-one" || item.Delegate.Name != "Agent One" {
		t.Errorf("delegate = %#v, want agent-one/Agent One", item.Delegate)
	}
}

// TestMapWorkItemHumanLeaseIsNotDelegate asserts a human (or service) lease
// holder is never surfaced as a delegate — only an agent lease qualifies.
func TestMapWorkItemHumanLeaseIsNotDelegate(t *testing.T) {
	resolve := stubActors(map[string]*store.Actor{
		"bob": {ID: "bob", Kind: "human", DisplayName: "Bob"},
	})
	fact := store.ProjectWorkFact{
		Task:  store.Task{ID: "WL-1", Title: "Ship it", State: "in_progress"},
		Lease: &store.Lease{ActorID: "bob"},
	}

	item, err := mapWorkItem(fact, false, resolve)
	if err != nil {
		t.Fatalf("mapWorkItem: %v", err)
	}
	if item.Delegate != nil {
		t.Errorf("delegate = %#v, want nil for a human lease holder", item.Delegate)
	}
}

// TestMapWorkItemMissingDisplayNameFallsBackToID asserts an actor with no
// display name renders its id instead — never an empty owner/delegate name.
func TestMapWorkItemMissingDisplayNameFallsBackToID(t *testing.T) {
	resolve := stubActors(map[string]*store.Actor{
		"svc-1": {ID: "svc-1", Kind: "human", DisplayName: ""},
	})
	fact := store.ProjectWorkFact{
		Task: store.Task{ID: "WL-1", Title: "Ship it", State: "ready", Assignee: "svc-1"},
	}

	item, err := mapWorkItem(fact, false, resolve)
	if err != nil {
		t.Fatalf("mapWorkItem: %v", err)
	}
	if item.Owner == nil || item.Owner.Name != "svc-1" {
		t.Errorf("owner = %#v, want name svc-1 (fallback to id)", item.Owner)
	}
}

// TestMapWorkItemNoAssigneeNoLease asserts an untouched task has neither
// owner nor delegate, and its evidence is declared (no backing event).
func TestMapWorkItemNoAssigneeNoLease(t *testing.T) {
	item, err := mapWorkItem(store.ProjectWorkFact{
		Task: store.Task{ID: "WL-1", Title: "Untouched", State: "ready"},
	}, false, stubActors(nil))
	if err != nil {
		t.Fatalf("mapWorkItem: %v", err)
	}
	if item.Owner != nil {
		t.Errorf("owner = %#v, want nil", item.Owner)
	}
	if item.Delegate != nil {
		t.Errorf("delegate = %#v, want nil", item.Delegate)
	}
	if item.StatusEvidence.Category != string(evidenceDeclared) {
		t.Errorf("status_evidence.category = %q, want declared", item.StatusEvidence.Category)
	}
}
