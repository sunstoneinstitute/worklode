package ui

import (
	"context"
	"strings"
	"testing"
	"time"
)

// renderMilestones renders the page and fails the test on a render error.
func renderMilestones(t *testing.T, v MilestonesView) string {
	t.Helper()
	var b strings.Builder
	if err := Milestones(v).Render(context.Background(), &b); err != nil {
		t.Fatalf("render Milestones: %v", err)
	}
	return b.String()
}

// TestMilestonesProgressIsPlainCounts pins the progress line spec 029 §2's
// derived counts render as: the numbers themselves, never a percentage or a
// bar the backbone has no basis for.
func TestMilestonesProgressIsPlainCounts(t *testing.T) {
	body := renderMilestones(t, MilestonesView{
		Page:    PageProps{Title: "Milestones"},
		Project: CockpitProject{ID: "worklode", Name: "Worklode backbone", Key: "WL"},
		Milestones: []MilestoneSection{{
			ID: "WL-MILE-1", Title: "Internal review",
			TasksTotal: 3, TasksClosed: 2, DeliverablesTotal: 2, DeliverablesLive: 1,
			Tasks: []MilestoneTaskRow{
				{ID: "WL-7", Title: "Fix the widget", State: "merged", Assignee: "ada"},
				{ID: "WL-8", Title: "Ship the widget", State: "ready"},
			},
			Deliverables: []DeliverableRow{{
				ID: "WL-DEL-1", Name: "Daily snapshot", CreatedAt: time.Unix(0, 0).UTC(),
				ReportedState: "published",
			}},
		}},
	})
	for _, want := range []string{
		"2/3 tasks closed", "1/2 deliverables live",
		"WL-MILE-1", "Internal review",
		`href="/tasks/WL-7"`, "Fix the widget", "ada",
		"Daily snapshot",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("milestone section missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "%") {
		t.Errorf("progress rendered a percentage the backbone cannot derive:\n%s", body)
	}
}

// TestMilestonesEmptyStatesAreHonest checks both empty states: a project with
// no milestones says so, and a milestone with no children says so instead of
// rendering empty tables.
func TestMilestonesEmptyStatesAreHonest(t *testing.T) {
	body := renderMilestones(t, MilestonesView{
		Page: PageProps{Title: "Milestones"}, Project: CockpitProject{ID: "worklode", Key: "WL"},
	})
	if !strings.Contains(body, "No milestones yet") {
		t.Errorf("empty page missing its honest empty state:\n%s", body)
	}
	if strings.Contains(body, "<table") {
		t.Errorf("empty page still renders a table:\n%s", body)
	}

	body = renderMilestones(t, MilestonesView{
		Page: PageProps{Title: "Milestones"}, Project: CockpitProject{ID: "worklode", Key: "WL"},
		Milestones: []MilestoneSection{{ID: "WL-MILE-2", Title: "Publication"}},
	})
	if strings.Contains(body, "No milestones yet") {
		t.Errorf("a populated page still renders the whole-page empty state:\n%s", body)
	}
	if !strings.Contains(body, "Nothing is attached to this milestone yet.") {
		t.Errorf("a childless milestone does not say so:\n%s", body)
	}
	if strings.Contains(body, "<table") {
		t.Errorf("a childless milestone still renders an empty table:\n%s", body)
	}
}

// TestMilestonesTaskTableScrollsInALabelledRegion pins spec 032 §10: the one
// table on this page scrolls inside its own labelled container rather than
// widening the page. The deliverable rows are not a table — they reuse the
// Deliverables page's row component, which reflows.
func TestMilestonesTaskTableScrollsInALabelledRegion(t *testing.T) {
	body := renderMilestones(t, MilestonesView{
		Page: PageProps{Title: "Milestones"}, Project: CockpitProject{ID: "worklode", Key: "WL"},
		Milestones: []MilestoneSection{{
			ID: "WL-MILE-1", Title: "Internal review", TasksTotal: 1,
			Tasks:        []MilestoneTaskRow{{ID: "WL-7", Title: "Fix the widget", State: "ready"}},
			Deliverables: []DeliverableRow{{ID: "WL-DEL-1", Name: "Daily snapshot", CreatedAt: time.Unix(0, 0).UTC()}},
		}},
	})
	const want = `<div class="tablewrap" role="region" aria-label="Internal review tasks" tabindex="0">`
	if !strings.Contains(body, want) {
		t.Errorf("missing scroll container %q:\n%s", want, body)
	}
}
