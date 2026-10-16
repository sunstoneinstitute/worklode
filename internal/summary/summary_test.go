package summary

import (
	"reflect"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

var today = time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)

func url(t store.SummaryTask) string { return "/t/" + t.ID }

func ids(items []model.SummaryItem) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.ID)
	}
	return out
}

func eq(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestEmptyProject(t *testing.T) {
	got := Derive(&store.SummaryFacts{}, today, url)
	if got.PastDue == nil || got.AwaitingReview == nil || got.Workloads == nil {
		t.Fatalf("lists must be non-nil: %+v", got)
	}
	if len(got.PastDue)+len(got.AwaitingReview)+len(got.Workloads) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestMemberWithNoTasks(t *testing.T) {
	f := &store.SummaryFacts{Roster: []store.Participant{{ActorID: "ann"}, {ActorID: "bob"}}}
	got := Derive(f, today, url)
	if len(got.Workloads) != 2 || got.Workloads[0].Actor != "ann" || got.Workloads[1].Actor != "bob" {
		t.Fatalf("roster order: %+v", got.Workloads)
	}
	w := got.Workloads[0]
	if w.Availability != "open" || w.Done != 0 || w.Total != 0 ||
		w.PastDue == nil || w.Upcoming == nil || w.AwaitingReview == nil {
		t.Errorf("empty workload: %+v", w)
	}
}

func TestDerive(t *testing.T) {
	f := &store.SummaryFacts{
		Roster: []store.Participant{{ActorID: "ann"}, {ActorID: "bob"}},
		Tasks: []store.SummaryTask{
			{ID: "WL-1", Title: "late newer", State: "in_progress", Assignee: "ann", Due: "2026-10-10"},
			{ID: "WL-2", Title: "late older", State: "ready", Assignee: "ann", Due: "2026-10-01"},
			{ID: "WL-3", Title: "due today", State: "ready", Assignee: "bob", Due: "2026-10-16"},
			{ID: "WL-4", Title: "old but closed", State: "merged", Assignee: "bob", Due: "2020-01-01", Closed: true},
			{ID: "WL-5", Title: "reviewing", State: "in_review", Assignee: "bob", Due: ""},
			{ID: "WL-6", Title: "no due", State: "ready", Assignee: "bob"},
		},
		Docs: []store.SummaryDoc{{Ref: "WL-SPEC-1", Title: "Spec", URL: "/d/1"}},
	}
	got := Derive(f, today, url)
	eq(t, "past_due", ids(got.PastDue), []string{"WL-2", "WL-1"})
	if got.PastDue[0].Label != "late older" || got.PastDue[0].Due != "2026-10-01" || got.PastDue[0].URL != "/t/WL-2" {
		t.Errorf("item: %+v", got.PastDue[0])
	}
	eq(t, "awaiting_review", ids(got.AwaitingReview), []string{"WL-5", "WL-SPEC-1"})
	if d := got.AwaitingReview[1]; d.Due != "" || d.URL != "/d/1" || d.Label != "Spec" {
		t.Errorf("doc item: %+v", d)
	}
	ann, bob := got.Workloads[0], got.Workloads[1]
	if ann.Availability != "overbooked" || bob.Availability != "open" {
		t.Errorf("availability: %s %s", ann.Availability, bob.Availability)
	}
	if bob.Done != 1 || bob.Total != 4 || ann.Done != 0 || ann.Total != 2 {
		t.Errorf("counts: ann %d/%d bob %d/%d", ann.Done, ann.Total, bob.Done, bob.Total)
	}
	eq(t, "bob past_due", ids(bob.PastDue), []string{})
	eq(t, "bob upcoming", ids(bob.Upcoming), []string{"WL-3"})
	eq(t, "bob awaiting", ids(bob.AwaitingReview), []string{"WL-5"})
}

func TestUpcomingCappedAtFiveSoonestFirst(t *testing.T) {
	f := &store.SummaryFacts{Roster: []store.Participant{{ActorID: "ann"}}}
	for i, d := range []string{"2026-12-07", "2026-12-01", "2026-12-06", "2026-12-02", "2026-12-05", "2026-12-03", "2026-12-04"} {
		f.Tasks = append(f.Tasks, store.SummaryTask{ID: "T" + string(rune('a'+i)), Assignee: "ann", State: "ready", Due: d})
	}
	w := Derive(f, today, url).Workloads[0]
	eq(t, "upcoming", ids(w.Upcoming), []string{"Tb", "Td", "Tf", "Tg", "Te"})
	if w.Total != 7 {
		t.Errorf("total = %d", w.Total)
	}
}
