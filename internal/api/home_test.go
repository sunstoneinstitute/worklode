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
