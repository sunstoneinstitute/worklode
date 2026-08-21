package api

import (
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestBucketWorkFacts exercises the per-project bucketing shared by the
// board and Home's card counts: in_progress and in_review bucket by state,
// a ready task with an open blocker is Blocked rather than Ready, and a
// done task appears in no bucket. Order within each bucket is preserved.
func TestBucketWorkFacts(t *testing.T) {
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
