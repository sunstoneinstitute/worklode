// inbox.go assembles the cross-project inbox page (spec 056 §3): what is
// waiting on the signed-in actor, in the six buckets §3.2 fixes the order of.
// The derivation is pure — everything it reads is fetched by the caller and
// handed in — so the bucket rules and the §3.3 ordering are testable without
// a database.
//
// Unrelated to inbox_mirror.go, which is spec 020's GitHub issue and
// pull-request triage import. They share a word and nothing else.
package api

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// inboxInputs is everything the inbox derivation reads, already fetched.
// Reviews are every open pr-kind approval org-wide with its project, PR
// title/URL, and author login; Facts are the all-projects work facts — fed
// whole so det-v1 resolves blocked-by chains across project boundaries (056
// §3.3) — and Membership/Led come from ProjectsForActor.
type inboxInputs struct {
	ActorID    string
	ActorLogin string // the actor's GitHub login, "" when none stored
	Reviews    []store.InboxReview
	Facts      []store.ProjectWorkFact
	Membership map[string]bool // project id -> member
	Led        map[string]bool // project id -> is_lead
	Now        time.Time
}

// activeTaskStates are the states §3.1 calls in-progress work. It matches
// store.HasInboxItems' state filter, which answers §4's indicator over the
// same six buckets.
var activeTaskStates = map[string]bool{"ready": true, "in_progress": true, "in_review": true}

// assembleInbox derives 056 §3.2's six buckets in order. Work buckets rank by
// rankConcernRoots over the full facts slice, filtered to membership
// afterwards — never before (§3.3). nil when every bucket is empty. Each item
// carries the text and href the page renders.
func assembleInbox(in inboxInputs) *ui.InboxView {
	reviews := slices.Clone(in.Reviews)
	slices.SortFunc(reviews, func(a, b store.InboxReview) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ApprovalID, b.ApprovalID)
	})

	var assignedReviews, unassignedLed, ownedReviews []ui.InboxItem
	for _, r := range reviews {
		item := reviewItem(r, in.Now)
		switch {
		case r.RequiredActor != nil && *r.RequiredActor == in.ActorID:
			assignedReviews = append(assignedReviews, item)
		case r.RequiredActor == nil && in.Led[r.Project]:
			unassignedLed = append(unassignedLed, item)
		case authored(r.AuthorLogin, in.ActorLogin) && r.RequiredActor != nil:
			// §3.1: owned means the actor's own pull request with somebody
			// else deciding. The first case already took a review required
			// of the actor themselves.
			ownedReviews = append(ownedReviews, item)
		}
	}

	pos := concernPositions(in.Facts, in.Membership, in.Now)
	var assignedWork, ownedWork, nearWork []store.ProjectWorkFact
	for _, f := range in.Facts {
		if !activeTaskStates[f.Task.State] {
			continue
		}
		switch {
		case f.Task.Assignee == in.ActorID:
			assignedWork = append(assignedWork, f)
		case f.Task.CreatedBy == in.ActorID:
			ownedWork = append(ownedWork, f)
		case in.Membership[f.Task.Project]:
			nearWork = append(nearWork, f)
		}
	}

	view := &ui.InboxView{Page: ui.PageProps{Title: "Inbox"}}
	add := func(label string, items []ui.InboxItem) {
		if len(items) > 0 {
			view.Buckets = append(view.Buckets, ui.InboxBucket{Label: label, Items: items})
		}
	}
	add("Reviews assigned to you", assignedReviews)
	add("Unassigned reviews in projects you lead", unassignedLed)
	add("Reviews you own", ownedReviews)
	add("Work assigned to you", workItems(assignedWork, pos, in.Now))
	add("Work you own", workItems(ownedWork, pos, in.Now))
	add("Other in-progress work", workItems(nearWork, pos, in.Now))
	if len(view.Buckets) == 0 {
		return nil
	}
	return view
}

// authored reports whether a pull request's GitHub login is the actor's.
// Empty never matches empty: an actor with no stored login owns nothing, and
// a pull request with no recorded author is owned by nobody (§3.1).
func authored(prAuthor, actorLogin string) bool {
	return prAuthor != "" && actorLogin != "" && strings.EqualFold(prAuthor, actorLogin)
}

// concernPositions maps each ready-and-blocked task to the position of the
// root-cause concern holding it, per §3.3: det-v1 scores the whole
// all-projects fact set, and only then are concerns dropped when nothing they
// hold sits in a project the actor belongs to. A task held by two roots takes
// the better position. Tasks no concern holds are absent.
func concernPositions(facts []store.ProjectWorkFact, membership map[string]bool, now time.Time) map[string]int {
	out := map[string]int{}
	rank := 0
	for _, r := range rankConcernRoots(facts, now) {
		member := false
		for _, h := range r.root.held {
			if membership[h.Task.Project] {
				member = true
				break
			}
		}
		if !member {
			continue
		}
		for id := range r.root.held {
			if _, seen := out[id]; !seen {
				out[id] = rank
			}
		}
		rank++
	}
	return out
}

// workItems renders a work bucket in §3.3's order: by the position of the
// root-cause concern holding the task, then the tasks no concern holds — a
// deterministic tail by priority rank then id, since det-v1 only ranks
// blocked roots.
func workItems(facts []store.ProjectWorkFact, pos map[string]int, now time.Time) []ui.InboxItem {
	slices.SortFunc(facts, func(a, b store.ProjectWorkFact) int {
		pa, hasA := pos[a.Task.ID]
		pb, hasB := pos[b.Task.ID]
		if hasA != hasB {
			if hasA {
				return -1
			}
			return 1
		}
		if hasA {
			if c := cmp.Compare(pa, pb); c != 0 {
				return c
			}
		}
		if c := cmp.Compare(priorityRank(a.Task.Priority), priorityRank(b.Task.Priority)); c != 0 {
			return c
		}
		return cmp.Compare(a.Task.ID, b.Task.ID)
	})
	out := make([]ui.InboxItem, 0, len(facts))
	for _, f := range facts {
		out = append(out, ui.InboxItem{
			Text:   f.Task.ID + " " + f.Task.Title,
			Href:   "/tasks/" + f.Task.ID,
			Detail: detail(f.Task.Project, f.Task.CreatedAt, now),
		})
	}
	return out
}

func reviewItem(r store.InboxReview, now time.Time) ui.InboxItem {
	text := r.Title
	if text == "" {
		text = r.EntityID
	}
	return ui.InboxItem{Text: text, Href: r.URL, Detail: detail(r.Project, r.CreatedAt, now)}
}

// detail is the muted second line on every row: the project and how long the
// item has been waiting.
func detail(project string, since, now time.Time) string {
	return project + " · " + humanAge(now.Sub(since))
}
