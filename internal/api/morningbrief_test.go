package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

func TestMorningBriefTierOf(t *testing.T) {
	cases := map[string]morningBriefTier{
		// tier 3: stopped or reached a bound
		"task.stopped":         briefTierStopped,
		"task.abandoned":       briefTierStopped,
		"lease.expired":        briefTierStopped,
		"runtime.crashloop":    briefTierStopped,
		"runtime.oom":          briefTierStopped,
		"runtime.flux_failure": briefTierStopped,
		// tier 2: material outcomes and changes
		"wl:DocumentSubmitted":  briefTierOutcome,
		"wl:DocumentAccepted":   briefTierOutcome,
		"task.done":             briefTierOutcome,
		"task.reopened":         briefTierOutcome,
		"deliverable.created":   briefTierOutcome,
		"approval.decided":      briefTierOutcome,
		"crew.member_added":     briefTierOutcome,
		"crew.member_removed":   briefTierOutcome,
		"issue.promoted":        briefTierOutcome,
		"runtime.flux_recovery": briefTierOutcome,
		// tier 4: everything else, routine, default
		"task.created":      briefTierRoutine,
		"push":              briefTierRoutine,
		"never.seen.before": briefTierRoutine,
	}

	for eventType, want := range cases {
		t.Run(eventType, func(t *testing.T) {
			got := morningBriefTierOf(eventType)
			if got != want {
				t.Errorf("morningBriefTierOf(%q) = %v, want %v", eventType, got, want)
			}
		})
	}
}

func TestMorningBriefProject(t *testing.T) {
	keyToProject := map[string]string{"WL": "worklode"}
	repoToProject := map[string]string{"sunstoneinstitute/worklode": "worklode"}

	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "payload project key wins",
			payload: `{"project": "explicit-project", "task": "WL-7"}`,
			want:    "explicit-project",
		},
		{
			name:    "task key resolves via keyToProject",
			payload: `{"task": "WL-7"}`,
			want:    "worklode",
		},
		{
			name:    "repository.full_name resolves via repoToProject",
			payload: `{"repository": {"full_name": "sunstoneinstitute/worklode"}}`,
			want:    "worklode",
		},
		{
			name:    "no attribution present",
			payload: `{"foo": "bar"}`,
			want:    "",
		},
		{
			name:    "non-object payload",
			payload: `"not an object"`,
			want:    "",
		},
		{
			name:    "unknown task prefix",
			payload: `{"task": "ZZ-1"}`,
			want:    "",
		},
		{
			name:    "unknown repository",
			payload: `{"repository": {"full_name": "someone/else"}}`,
			want:    "",
		},
		{
			name:    "task key without dash",
			payload: `{"task": "notaslug"}`,
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := store.Event{Payload: []byte(tc.payload)}
			got := morningBriefProject(ev, keyToProject, repoToProject)
			if got != tc.want {
				t.Errorf("morningBriefProject(%s) = %q, want %q", tc.payload, got, tc.want)
			}
		})
	}
}

func TestAssembleMorningBrief(t *testing.T) {
	proj1 := map[string]store.Project{
		"proj1": {ID: "proj1", Name: "Proj One", FocusNote: "Ship the thing"},
	}
	order1 := []string{"proj1"}
	keyToProject := map[string]string{"WL": "proj1"}

	cases := []struct {
		name  string
		in    morningBriefInputs
		check func(t *testing.T, got *ui.MorningBriefView)
	}{
		{
			// the §9 order: a group with all four tiers renders NeedsYou,
			// then Outcomes, then Stopped, then the Routine count — assert
			// field placement, not string order.
			name: "order",
			in: morningBriefInputs{
				Events: []store.Event{
					{ID: 1, Type: "task.done", Payload: []byte(`{"task":"WL-1"}`)},
					{ID: 2, Type: "task.stopped", Payload: []byte(`{"task":"WL-2"}`)},
					{ID: 3, Type: "task.created", Payload: []byte(`{"task":"WL-3"}`)},
				},
				Boundary:     0,
				Order:        order1,
				Projects:     proj1,
				Awaiting:     map[string]int{"proj1": 2},
				Assigned:     map[string][]store.OwnedWork{"proj1": {{Kind: "task", ID: "WL-9", Title: "Fix thing", State: "in_progress"}}},
				KeyToProject: keyToProject,
			},
			check: func(t *testing.T, got *ui.MorningBriefView) {
				if got == nil {
					t.Fatal("got nil, want a view")
				}
				if len(got.Groups) != 1 {
					t.Fatalf("Groups = %d, want 1", len(got.Groups))
				}
				g := got.Groups[0]
				if g.FocusNote != "Ship the thing" {
					t.Errorf("FocusNote = %q, want %q", g.FocusNote, "Ship the thing")
				}
				if len(g.NeedsYou) != 2 {
					t.Errorf("NeedsYou = %d, want 2", len(g.NeedsYou))
				}
				if len(g.Outcomes) != 1 {
					t.Errorf("Outcomes = %d, want 1", len(g.Outcomes))
				}
				if len(g.Stopped) != 1 {
					t.Errorf("Stopped = %d, want 1", len(g.Stopped))
				}
				if g.Routine != 1 {
					t.Errorf("Routine = %d, want 1", g.Routine)
				}
			},
		},
		{
			// collapse: five task.created/push events → Routine: 5, no items.
			name: "collapse",
			in: morningBriefInputs{
				Events: []store.Event{
					{ID: 1, Type: "task.created", Payload: []byte(`{"task":"WL-1"}`)},
					{ID: 2, Type: "push", Payload: []byte(`{"task":"WL-1"}`)},
					{ID: 3, Type: "task.created", Payload: []byte(`{"task":"WL-2"}`)},
					{ID: 4, Type: "push", Payload: []byte(`{"task":"WL-2"}`)},
					{ID: 5, Type: "task.created", Payload: []byte(`{"task":"WL-3"}`)},
				},
				Boundary:     0,
				Order:        order1,
				Projects:     proj1,
				KeyToProject: keyToProject,
			},
			check: func(t *testing.T, got *ui.MorningBriefView) {
				if got == nil {
					t.Fatal("got nil, want a view")
				}
				if len(got.Groups) != 1 {
					t.Fatalf("Groups = %d, want 1", len(got.Groups))
				}
				g := got.Groups[0]
				if len(g.NeedsYou) != 0 || len(g.Outcomes) != 0 || len(g.Stopped) != 0 {
					t.Errorf("expected only Routine, got NeedsYou=%d Outcomes=%d Stopped=%d",
						len(g.NeedsYou), len(g.Outcomes), len(g.Stopped))
				}
				if g.Routine != 5 {
					t.Errorf("Routine = %d, want 5", g.Routine)
				}
			},
		},
		{
			// persistence: Awaiting and Assigned populate NeedsYou even with
			// Events empty (CanReview false, Cutoff == Boundary) — an
			// undecided approval must not vanish from the brief after a
			// review advance.
			name: "persistence",
			in: morningBriefInputs{
				Boundary: 42,
				Order:    order1,
				Projects: proj1,
				Awaiting: map[string]int{"proj1": 1},
				Assigned: map[string][]store.OwnedWork{"proj1": {{Kind: "task", ID: "WL-9", Title: "Fix thing", State: "in_progress"}}},
			},
			check: func(t *testing.T, got *ui.MorningBriefView) {
				if got == nil {
					t.Fatal("got nil, want a view")
				}
				if got.CanReview {
					t.Error("CanReview = true, want false")
				}
				if got.Cutoff != 42 {
					t.Errorf("Cutoff = %d, want 42 (== Boundary)", got.Cutoff)
				}
				if len(got.Groups) != 1 || len(got.Groups[0].NeedsYou) != 2 {
					t.Fatalf("Groups = %+v, want 1 group with 2 NeedsYou items", got.Groups)
				}
			},
		},
		{
			// scope: an event attributed to a project absent from Order is
			// dropped; an unattributed event is dropped but still counted
			// in Shown and still moves Cutoff.
			name: "scope",
			in: morningBriefInputs{
				Events: []store.Event{
					{ID: 1, Type: "task.done", Payload: []byte(`{"project":"other-project"}`)},
					{ID: 2, Type: "task.done", Payload: []byte(`{}`)},
				},
				Boundary:     0,
				Order:        order1,
				Projects:     proj1,
				KeyToProject: keyToProject,
			},
			check: func(t *testing.T, got *ui.MorningBriefView) {
				if got == nil {
					t.Fatal("got nil, want a view")
				}
				if got.Shown != 2 {
					t.Errorf("Shown = %d, want 2", got.Shown)
				}
				if got.Cutoff != 2 {
					t.Errorf("Cutoff = %d, want 2", got.Cutoff)
				}
				if len(got.Groups) != 0 {
					t.Errorf("Groups = %+v, want none", got.Groups)
				}
			},
		},
		{
			// cutoff: Cutoff = max event id; empty events → Boundary;
			// Truncated passes through.
			name: "cutoff with events",
			in: morningBriefInputs{
				Events: []store.Event{
					{ID: 5, Type: "task.done", Payload: []byte(`{"task":"WL-1"}`)},
					{ID: 9, Type: "task.done", Payload: []byte(`{"task":"WL-1"}`)},
				},
				Boundary:     3,
				Order:        order1,
				Projects:     proj1,
				KeyToProject: keyToProject,
				Truncated:    true,
			},
			check: func(t *testing.T, got *ui.MorningBriefView) {
				if got == nil {
					t.Fatal("got nil, want a view")
				}
				if got.Cutoff != 9 {
					t.Errorf("Cutoff = %d, want 9 (max event id)", got.Cutoff)
				}
				if !got.Truncated {
					t.Error("Truncated = false, want true (passthrough)")
				}
			},
		},
		{
			name: "cutoff with no events",
			in: morningBriefInputs{
				Boundary: 7,
				Order:    order1,
				Projects: proj1,
				Awaiting: map[string]int{"proj1": 1},
			},
			check: func(t *testing.T, got *ui.MorningBriefView) {
				if got == nil {
					t.Fatal("got nil, want a view")
				}
				if got.Cutoff != 7 {
					t.Errorf("Cutoff = %d, want 7 (== Boundary)", got.Cutoff)
				}
			},
		},
		{
			// nil: no state, no events → nil.
			name: "nil",
			in: morningBriefInputs{
				Boundary: 3,
				Order:    order1,
				Projects: proj1,
			},
			check: func(t *testing.T, got *ui.MorningBriefView) {
				if got != nil {
					t.Errorf("got %+v, want nil", got)
				}
			},
		},
		{
			// empty-but-advanced: events exist but all drop → non-nil view,
			// CanReview true, zero groups.
			name: "empty-but-advanced",
			in: morningBriefInputs{
				Events: []store.Event{
					{ID: 1, Type: "task.done", Payload: []byte(`{}`)},
				},
				Boundary: 0,
				Order:    order1,
				Projects: proj1,
			},
			check: func(t *testing.T, got *ui.MorningBriefView) {
				if got == nil {
					t.Fatal("got nil, want a non-nil view")
				}
				if !got.CanReview {
					t.Error("CanReview = false, want true")
				}
				if len(got.Groups) != 0 {
					t.Errorf("Groups = %+v, want none", got.Groups)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := assembleMorningBrief(tc.in)
			tc.check(t, got)
		})
	}
}

// TestBriefEventsSince seeds more events than one store.ListEvents page (200)
// but fewer than morningBriefEventCap (2000), and checks briefEventsSince
// pages across that boundary transparently: a fetch from 0 returns every
// seeded event in ascending id order and reports no truncation, and a fetch
// cursored mid-log returns only the tail after it.
func TestBriefEventsSince(t *testing.T) {
	t.Parallel()
	st := store.OpenTestStore(t)
	ctx := context.Background()
	s := &server{st: st}

	const n = 250 // > store.MaxEventListLimit (200), < morningBriefEventCap (2000)
	var ids []int64
	for i := 0; i < n; i++ {
		id, _, err := st.RecordEvent(ctx, "system", fmt.Sprintf("%s-%d", t.Name(), i), "test.event", nil, nil)
		if err != nil {
			t.Fatalf("record event %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	events, truncated := pollBriefEventsSince(t, ctx, s, 0, n)
	if len(events) != n {
		t.Fatalf("briefEventsSince(0) returned %d events, want %d", len(events), n)
	}
	if truncated {
		t.Error("truncated = true, want false: 250 events is well under the 2000 cap")
	}
	for i, ev := range events {
		if ev.ID != ids[i] {
			t.Fatalf("events[%d].ID = %d, want %d (ascending id order)", i, ev.ID, ids[i])
		}
	}

	// A cursor mid-log returns only the tail after it. The full log is
	// already known visible from the poll above, so this read needs no
	// further polling.
	mid := events[100].ID
	tail, truncated, err := s.briefEventsSince(ctx, mid)
	if err != nil {
		t.Fatalf("briefEventsSince(after=%d): %v", mid, err)
	}
	if truncated {
		t.Error("truncated = true, want false")
	}
	if want := n - 101; len(tail) != want {
		t.Fatalf("briefEventsSince(after=%d) returned %d events, want %d", mid, len(tail), want)
	}
	if tail[0].ID != events[101].ID {
		t.Fatalf("tail[0].ID = %d, want %d", tail[0].ID, events[101].ID)
	}
}

// pollBriefEventsSince retries briefEventsSince until it sees at least want
// events or a 10s deadline passes. Needed because the commit horizon
// briefEventsSince reads through (store.ListEvents's eventHorizon) can be
// held back by a concurrent transaction elsewhere on the same Postgres
// instance, same as store.pollListEvents in events_test.go.
func pollBriefEventsSince(t *testing.T, ctx context.Context, s *server, after int64, want int) (events []store.Event, truncated bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var err error
		events, truncated, err = s.briefEventsSince(ctx, after)
		if err != nil {
			t.Fatalf("briefEventsSince: %v", err)
		}
		if len(events) >= want {
			return events, truncated
		}
		if time.Now().After(deadline) {
			t.Fatalf("briefEventsSince: got %d events after polling, want %d "+
				"(commit horizon held back by a concurrent transaction elsewhere on the instance?)",
				len(events), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
