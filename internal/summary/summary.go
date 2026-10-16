// Package summary derives the WL-REQ-1797 project summary from store facts.
// Pure: no I/O and no clock. The caller passes today and the task URL builder.
package summary

import (
	"cmp"
	"slices"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const upcomingMax = 5

// Derive builds the summary. Task Due is a YYYY-MM-DD string or empty, which
// compares correctly as text; an empty Due is neither past due nor upcoming.
func Derive(f *store.SummaryFacts, today time.Time, taskURL func(store.SummaryTask) string) model.ProjectSummary {
	day := today.Format("2006-01-02")
	item := func(t store.SummaryTask) model.SummaryItem {
		return model.SummaryItem{ID: t.ID, Label: t.Title, Due: t.Due, URL: taskURL(t)}
	}
	byDue := func(a, b store.SummaryTask) int { return cmp.Or(cmp.Compare(a.Due, b.Due), cmp.Compare(a.ID, b.ID)) }
	pastDue := func(t store.SummaryTask) bool { return !t.Closed && t.Due != "" && t.Due < day }

	// lists returns the past-due, upcoming and in-review items of tasks.
	lists := func(tasks []store.SummaryTask) (past, up, review []model.SummaryItem) {
		past, up, review = []model.SummaryItem{}, []model.SummaryItem{}, []model.SummaryItem{}
		sorted := slices.SortedFunc(slices.Values(tasks), byDue)
		for _, t := range sorted {
			switch {
			case t.Closed:
			case pastDue(t):
				past = append(past, item(t))
			case t.Due != "" && len(up) < upcomingMax:
				up = append(up, item(t))
			}
		}
		for _, t := range tasks {
			if !t.Closed && t.State == "in_review" {
				review = append(review, item(t))
			}
		}
		return
	}

	out := model.ProjectSummary{Workloads: []model.Workload{}}
	out.PastDue, _, out.AwaitingReview = lists(f.Tasks)
	for _, d := range f.Docs {
		out.AwaitingReview = append(out.AwaitingReview, model.SummaryItem{ID: d.Ref, Label: d.Title, URL: d.URL})
	}
	for _, m := range f.Roster {
		var mine []store.SummaryTask
		w := model.Workload{Actor: m.ActorID}
		for _, t := range f.Tasks {
			if t.Assignee != m.ActorID {
				continue
			}
			mine = append(mine, t)
			w.Total++
			if t.Closed {
				w.Done++
			}
		}
		w.PastDue, w.Upcoming, w.AwaitingReview = lists(mine)
		w.Availability = "open"
		if len(w.PastDue) > 0 {
			w.Availability = "overbooked"
		}
		out.Workloads = append(out.Workloads, w)
	}
	return out
}
