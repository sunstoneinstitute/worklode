package api

import (
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
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
		Task:  model.Task{ID: "WL-1", Title: "Ship it", State: "in_progress", Assignee: "dana"},
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
		Task:  model.Task{ID: "WL-1", Title: "Ship it", State: "in_progress"},
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
		Task: model.Task{ID: "WL-1", Title: "Ship it", State: "ready", Assignee: "svc-1"},
	}

	item, err := mapWorkItem(fact, false, resolve)
	if err != nil {
		t.Fatalf("mapWorkItem: %v", err)
	}
	if item.Owner == nil || item.Owner.Name != "svc-1" {
		t.Errorf("owner = %#v, want name svc-1 (fallback to id)", item.Owner)
	}
}

// notFoundActors is a resolveActor func that reports every non-empty id as
// store.ErrNotFound — the real GetActor's signal for "no such actor", which
// pinnedBySummary must treat as a fallback, not a hard failure.
func notFoundActors() func(string) (*store.Actor, error) {
	return func(id string) (*store.Actor, error) {
		if id == "" {
			return nil, nil
		}
		return nil, store.ErrNotFound
	}
}

// TestBuildPinnedFocusUnsetIsNil asserts a project with no focus note yields a
// nil pinned-focus card, never a dummy record — the "nil when unset" contract.
func TestBuildPinnedFocusUnsetIsNil(t *testing.T) {
	got, err := buildPinnedFocus(&store.Project{}, stubActors(nil))
	if err != nil {
		t.Fatalf("buildPinnedFocus: %v", err)
	}
	if got != nil {
		t.Errorf("pinned focus = %#v, want nil for an unset note", got)
	}
}

// TestBuildPinnedFocusResolvedActor asserts a pinned-by that resolves to an
// actor carries the actor's id and display name (not the bare id).
func TestBuildPinnedFocusResolvedActor(t *testing.T) {
	at := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	resolve := stubActors(map[string]*store.Actor{
		"stig": {ID: "stig", Kind: "human", DisplayName: "Stig Bakken"},
	})
	got, err := buildPinnedFocus(&store.Project{
		FocusNote: "Ship the cockpit", FocusPinnedBy: "stig", FocusPinnedAt: at,
	}, resolve)
	if err != nil {
		t.Fatalf("buildPinnedFocus: %v", err)
	}
	if got == nil || got.Note != "Ship the cockpit" || !got.PinnedAt.Equal(at) {
		t.Fatalf("pinned focus = %#v, want note/at populated", got)
	}
	if got.PinnedBy == nil || got.PinnedBy.ID != "stig" || got.PinnedBy.Name != "Stig Bakken" {
		t.Errorf("pinned_by = %#v, want stig/Stig Bakken", got.PinnedBy)
	}
}

// TestBuildPinnedFocusUnresolvedFallback asserts a pinned-by that resolves to
// no actor falls back to the raw string as the display name and never fails
// the projection — covering both the (nil, nil) and (nil, ErrNotFound) shapes
// resolveActor can return for an unknown pinner.
func TestBuildPinnedFocusUnresolvedFallback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		resolve func(string) (*store.Actor, error)
	}{
		{"nil actor no error", stubActors(nil)},
		{"ErrNotFound", notFoundActors()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildPinnedFocus(&store.Project{
				FocusNote: "Keep going", FocusPinnedBy: "A Seeded Name",
			}, tc.resolve)
			if err != nil {
				t.Fatalf("buildPinnedFocus: %v", err)
			}
			if got == nil || got.PinnedBy == nil {
				t.Fatalf("pinned focus = %#v, want a pinned_by fallback", got)
			}
			if got.PinnedBy.ID != "" || got.PinnedBy.Name != "A Seeded Name" {
				t.Errorf("pinned_by = %#v, want empty id and the raw name", got.PinnedBy)
			}
		})
	}
}

// TestBuildPinnedFocusNoPinner asserts a note with an empty pinned-by yields a
// card with a nil PinnedBy — a note can stand without a named pinner.
func TestBuildPinnedFocusNoPinner(t *testing.T) {
	got, err := buildPinnedFocus(&store.Project{FocusNote: "Solo note"}, notFoundActors())
	if err != nil {
		t.Fatalf("buildPinnedFocus: %v", err)
	}
	if got == nil || got.Note != "Solo note" {
		t.Fatalf("pinned focus = %#v, want the note", got)
	}
	if got.PinnedBy != nil {
		t.Errorf("pinned_by = %#v, want nil for an empty pinner", got.PinnedBy)
	}
}

// TestBuildNextDecision asserts the next-decision card is nil when unset and
// carries title/accountable/readiness when a title is set.
func TestBuildNextDecision(t *testing.T) {
	if got := buildNextDecision(&store.Project{}); got != nil {
		t.Errorf("next decision = %#v, want nil for an unset title", got)
	}
	got := buildNextDecision(&store.Project{
		DecisionTitle: "Pick a datastore", DecisionAccountable: "stig",
		DecisionReadiness: "blocked on benchmark",
	})
	if got == nil || got.Title != "Pick a datastore" ||
		got.Accountable != "stig" || got.Readiness != "blocked on benchmark" {
		t.Errorf("next decision = %#v, want the three fields populated", got)
	}
}

// TestMapWorkItemNoAssigneeNoLease asserts an untouched task has neither
// owner nor delegate, and its evidence is declared (no backing event).
func TestMapWorkItemNoAssigneeNoLease(t *testing.T) {
	item, err := mapWorkItem(store.ProjectWorkFact{
		Task: model.Task{ID: "WL-1", Title: "Untouched", State: "ready"},
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

// TestRankSecondaryConcernsNamesBlockingPlan: a ready task held only by a
// plan ordered before its own (025 §9.3) is blocked and its root-cause
// concern names that plan, even when it is still draft and has minted no
// task to name. See cockpit_rank_test.go for det-v1's fuller coverage.
func TestRankSecondaryConcernsNamesBlockingPlan(t *testing.T) {
	f := store.ProjectWorkFact{
		Task: model.Task{ID: "WL-2", State: "ready"},
		BlockingPlans: []model.DocRef{
			{ID: 7, Slug: "plan-a", Title: "Plan A", Status: "draft"},
		},
	}
	if !f.Blocked() {
		t.Fatalf("Blocked() = false for a task a plan holds, which Claim refuses")
	}
	got := rankSecondaryConcerns([]store.ProjectWorkFact{f}, time.Now())
	if len(got) != 1 || got[0].Kind != "blocker" || got[0].Title != "Plan A" || got[0].URL != "/docs/7" {
		t.Fatalf("concerns = %#v, want one blocker naming Plan A at /docs/7", got)
	}
	if !strings.Contains(got[0].Evidence.Summary, "plan-a") {
		t.Errorf("evidence = %q, want it to name the blocking plan (slug plan-a)", got[0].Evidence.Summary)
	}
}
