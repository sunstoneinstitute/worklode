package api

import (
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// TestBucketWorkFacts exercises the per-project bucketing shared by the
// board and Home's card counts: in_progress and in_review bucket by state,
// a ready task with an open blocker is Blocked rather than Ready, and a
// done task appears in no bucket. Order within each bucket is preserved.
func TestBucketWorkFacts(t *testing.T) {
	t.Parallel()
	facts := []store.ProjectWorkFact{
		{Task: model.Task{ID: "WL-1", State: "in_progress"}},
		{Task: model.Task{ID: "WL-2", State: "in_review"}},
		{Task: model.Task{ID: "WL-3", State: "ready"}},
		{Task: model.Task{ID: "WL-4", State: "ready"},
			OpenBlockers: []store.TaskRef{{ID: "WL-3"}}},
		{Task: model.Task{ID: "WL-5", State: "done"}},
	}

	b := bucketWorkFacts(facts)

	ids := func(fs []store.ProjectWorkFact) []string {
		out := make([]string, len(fs))
		for i, f := range fs {
			out[i] = f.Task.ID
		}
		return out
	}

	assertIDs := func(t *testing.T, label string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s = %v, want %v", label, got, want)
			}
		}
	}

	assertIDs(t, "InProgress", ids(b.InProgress), "WL-1")
	assertIDs(t, "InReview", ids(b.InReview), "WL-2")
	assertIDs(t, "Ready", ids(b.Ready), "WL-3")
	assertIDs(t, "Blocked", ids(b.Blocked), "WL-4")

	for _, bucket := range [][]store.ProjectWorkFact{b.InProgress, b.InReview, b.Ready, b.Blocked} {
		for _, f := range bucket {
			if f.Task.ID == "WL-5" {
				t.Fatalf("done task WL-5 appeared in a bucket")
			}
		}
	}
}

// TestLastActivity checks the newest Task.UpdatedAt across facts of any
// state (done included), and the zero time for an empty slice.
func TestLastActivity(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		facts []store.ProjectWorkFact
		want  time.Time
	}{
		{
			name:  "empty",
			facts: nil,
			want:  time.Time{},
		},
		{
			name: "mixed states, newest belongs to a done task",
			facts: []store.ProjectWorkFact{
				{Task: model.Task{ID: "WL-1", State: "in_progress", UpdatedAt: base}},
				{Task: model.Task{ID: "WL-2", State: "ready", UpdatedAt: base.Add(-time.Hour)}},
				{Task: model.Task{ID: "WL-3", State: "done", UpdatedAt: base.Add(time.Hour)}},
			},
			want: base.Add(time.Hour),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lastActivity(tt.facts)
			if !got.Equal(tt.want) {
				t.Fatalf("lastActivity() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAssembleHomeFacts exercises the pure projection deciding which
// projects get a card and with what facts, per task-3-brief.md.
func TestAssembleHomeFacts(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	member := store.Project{ID: "proj-member", Name: "Member Project"}
	lead := store.Project{ID: "proj-lead", Name: "Lead Project"}
	nonMemberAwaiting := store.Project{ID: "proj-awaiting", Name: "Awaiting Project"}
	unrelated := store.Project{ID: "proj-unrelated", Name: "Unrelated Project"}

	t.Run("actor mode: member, lead, and non-member-with-awaiting get cards; unrelated does not", func(t *testing.T) {
		in := homeInputs{
			Projects: []store.Project{member, lead, nonMemberAwaiting, unrelated},
			Membership: map[string]memberFacts{
				"proj-member": {IsLead: false},
				"proj-lead":   {IsLead: true},
			},
			Awaiting: map[string]int{
				"proj-awaiting": 2,
			},
		}

		got := assembleHomeFacts(in)

		byID := make(map[string]homeCardFacts, len(got))
		for _, c := range got {
			byID[c.Project.ID] = c
		}

		if _, ok := byID["proj-unrelated"]; ok {
			t.Fatalf("unrelated project got a card: %+v", got)
		}

		m, ok := byID["proj-member"]
		if !ok {
			t.Fatalf("member project got no card")
		}
		if !m.IsMember || m.IsLead {
			t.Fatalf("member card = %+v, want IsMember=true IsLead=false", m)
		}

		l, ok := byID["proj-lead"]
		if !ok {
			t.Fatalf("lead project got no card")
		}
		if !l.IsMember || !l.IsLead {
			t.Fatalf("lead card = %+v, want IsMember=true IsLead=true", l)
		}

		a, ok := byID["proj-awaiting"]
		if !ok {
			t.Fatalf("non-member project with Awaiting>0 got no card")
		}
		if a.IsMember || a.IsLead || a.Awaiting != 2 {
			t.Fatalf("awaiting card = %+v, want IsMember=false IsLead=false Awaiting=2", a)
		}
	})

	t.Run("open mode: every project gets a card, no role, Awaiting ignored", func(t *testing.T) {
		in := homeInputs{
			Projects: []store.Project{member, lead},
			Membership: map[string]memberFacts{
				"proj-member": {IsLead: false},
				"proj-lead":   {IsLead: true},
			},
			Awaiting: map[string]int{
				"proj-member": 5,
				"proj-lead":   7,
			},
			OpenMode: true,
		}

		got := assembleHomeFacts(in)

		if len(got) != 2 {
			t.Fatalf("assembleHomeFacts() = %d cards, want 2", len(got))
		}
		for _, c := range got {
			if c.IsMember || c.IsLead {
				t.Fatalf("open mode card %+v has a role, want none", c)
			}
			if c.Awaiting != 0 {
				t.Fatalf("open mode card %+v has Awaiting != 0, want the map ignored entirely", c)
			}
		}
	})

	t.Run("actor on no projects and nothing awaiting: empty slice, never fabricated", func(t *testing.T) {
		in := homeInputs{
			Projects:   []store.Project{unrelated},
			Membership: map[string]memberFacts{},
			Awaiting:   map[string]int{},
		}

		got := assembleHomeFacts(in)

		if len(got) != 0 {
			t.Fatalf("assembleHomeFacts() = %+v, want empty slice", got)
		}
	})

	t.Run("counts respect bucketing rules: blocked ready task and done task moving activity", func(t *testing.T) {
		facts := []store.ProjectWorkFact{
			{Task: model.Task{ID: "WL-1", State: "in_progress", UpdatedAt: base}},
			{Task: model.Task{ID: "WL-2", State: "in_review", UpdatedAt: base}},
			{Task: model.Task{ID: "WL-3", State: "ready", UpdatedAt: base}},
			{Task: model.Task{ID: "WL-4", State: "ready", UpdatedAt: base},
				OpenBlockers: []store.TaskRef{{ID: "WL-3"}}},
			{Task: model.Task{ID: "WL-5", State: "done", UpdatedAt: base.Add(time.Hour)}},
		}

		in := homeInputs{
			Projects: []store.Project{member},
			Facts: map[string][]store.ProjectWorkFact{
				"proj-member": facts,
			},
			Membership: map[string]memberFacts{
				"proj-member": {IsLead: false},
			},
		}

		got := assembleHomeFacts(in)
		if len(got) != 1 {
			t.Fatalf("assembleHomeFacts() = %d cards, want 1", len(got))
		}
		c := got[0]
		if c.InProgress != 1 || c.InReview != 1 || c.Blocked != 1 {
			t.Fatalf("card counts = %+v, want InProgress=1 InReview=1 Blocked=1", c)
		}
		if !c.LastActivity.Equal(base.Add(time.Hour)) {
			t.Fatalf("LastActivity = %v, want %v (the done task's timestamp)", c.LastActivity, base.Add(time.Hour))
		}
	})

	t.Run("project with no tasks: zero LastActivity", func(t *testing.T) {
		in := homeInputs{
			Projects: []store.Project{member},
			Membership: map[string]memberFacts{
				"proj-member": {IsLead: false},
			},
		}

		got := assembleHomeFacts(in)
		if len(got) != 1 {
			t.Fatalf("assembleHomeFacts() = %d cards, want 1", len(got))
		}
		if !got[0].LastActivity.IsZero() {
			t.Fatalf("LastActivity = %v, want zero", got[0].LastActivity)
		}
	})
}

// cardIDs is a test helper for readable failure messages.
func cardIDs(cards []ui.HomeCard) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ProjectID
	}
	return out
}

// TestHomeCardsOrderAndSignals exercises the pure derivation on top of
// assembleHomeFacts per task-4-brief.md: tier, the exact signal strings, the
// role badge, crew-initial truncation, and the sort order.
func TestHomeCardsOrderAndSignals(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	factsAt := func(at time.Time) []store.ProjectWorkFact {
		return []store.ProjectWorkFact{{Task: model.Task{ID: "x", State: "done", UpdatedAt: at}}}
	}

	t.Run("actor mode: tier order, exact signals (including singular), and role badges", func(t *testing.T) {
		await2 := store.Project{ID: "proj-await2", Name: "Awaiting Two"}
		await1 := store.Project{ID: "proj-await1", Name: "Awaiting One"}
		lead := store.Project{ID: "proj-lead", Name: "Lead Project"}
		member := store.Project{ID: "proj-member", Name: "Member Project"}

		in := homeInputs{
			Projects: []store.Project{await2, await1, lead, member},
			Facts: map[string][]store.ProjectWorkFact{
				"proj-await2": factsAt(base.Add(1 * time.Hour)),
				"proj-await1": factsAt(base),
				"proj-lead":   factsAt(base.Add(2 * time.Hour)),
				"proj-member": factsAt(base.Add(3 * time.Hour)),
			},
			Membership: map[string]memberFacts{
				"proj-lead":   {IsLead: true},
				"proj-member": {IsLead: false},
			},
			Awaiting: map[string]int{
				"proj-await2": 2,
				"proj-await1": 1,
			},
		}

		got := homeCards(in)

		wantIDs := []string{"proj-await2", "proj-await1", "proj-lead", "proj-member"}
		if len(got) != len(wantIDs) {
			t.Fatalf("homeCards() = %d cards, want %d (got %v)", len(got), len(wantIDs), cardIDs(got))
		}
		for i, id := range wantIDs {
			if got[i].ProjectID != id {
				t.Fatalf("card order = %v, want %v", cardIDs(got), wantIDs)
			}
		}

		if got[0].Signal != "2 approvals awaiting you" || got[0].RoleBadge != "" {
			t.Fatalf("await2 card = %+v, want Signal=%q RoleBadge=%q", got[0], "2 approvals awaiting you", "")
		}
		if got[1].Signal != "1 approval awaiting you" || got[1].RoleBadge != "" {
			t.Fatalf("await1 card = %+v, want Signal=%q RoleBadge=%q", got[1], "1 approval awaiting you", "")
		}
		if got[2].Signal != "You lead this project" || got[2].RoleBadge != "Lead" {
			t.Fatalf("lead card = %+v, want Signal=%q RoleBadge=%q", got[2], "You lead this project", "Lead")
		}
		if got[3].Signal != "You are on this project" || got[3].RoleBadge != "Member" {
			t.Fatalf("member card = %+v, want Signal=%q RoleBadge=%q", got[3], "You are on this project", "Member")
		}
	})

	t.Run("open mode: last-activity descending, ID tiebreak, every Signal/RoleBadge empty", func(t *testing.T) {
		projB := store.Project{ID: "proj-b", Name: "B"}
		projA := store.Project{ID: "proj-a", Name: "A"}
		projC := store.Project{ID: "proj-c", Name: "C"}

		in := homeInputs{
			Projects: []store.Project{projB, projA, projC},
			Facts: map[string][]store.ProjectWorkFact{
				"proj-b": factsAt(base),                // tied with proj-a
				"proj-a": factsAt(base),                // tied with proj-b -> ID tiebreak
				"proj-c": factsAt(base.Add(time.Hour)), // most recent
			},
			OpenMode: true,
		}

		got := homeCards(in)

		wantIDs := []string{"proj-c", "proj-a", "proj-b"}
		if len(got) != len(wantIDs) {
			t.Fatalf("homeCards() = %d cards, want %d (got %v)", len(got), len(wantIDs), cardIDs(got))
		}
		for i, id := range wantIDs {
			if got[i].ProjectID != id {
				t.Fatalf("card order = %v, want %v", cardIDs(got), wantIDs)
			}
			if got[i].Signal != "" {
				t.Fatalf("open mode card %+v has non-empty Signal", got[i])
			}
			if got[i].RoleBadge != "" {
				t.Fatalf("open mode card %+v has non-empty RoleBadge", got[i])
			}
		}
	})

	t.Run("crew truncation: seven names, five initials, CrewMore=2, lead-first order preserved", func(t *testing.T) {
		names := []string{"Ada Lovelace", "Bob Smith", "Cara Diaz", "Dan Ok", "Eve Chan", "Fay Wong", "Gus Lee"}
		p := store.Project{ID: "proj-crew", Name: "Crew Project"}

		in := homeInputs{
			Projects: []store.Project{p},
			Membership: map[string]memberFacts{
				"proj-crew": {IsLead: false},
			},
			Participants: map[string][]string{
				"proj-crew": names,
			},
		}

		got := homeCards(in)
		if len(got) != 1 {
			t.Fatalf("homeCards() = %d cards, want 1", len(got))
		}

		c := got[0]
		wantInitials := []string{"AL", "BS", "CD", "DO", "EC"}
		if len(c.CrewInitials) != len(wantInitials) {
			t.Fatalf("CrewInitials = %v, want %v", c.CrewInitials, wantInitials)
		}
		for i, w := range wantInitials {
			if c.CrewInitials[i] != w {
				t.Fatalf("CrewInitials = %v, want %v", c.CrewInitials, wantInitials)
			}
		}
		if c.CrewMore != 2 {
			t.Fatalf("CrewMore = %d, want 2", c.CrewMore)
		}
	})
}
