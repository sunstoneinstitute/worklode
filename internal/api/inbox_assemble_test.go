package api

import (
	"strconv"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// These are inbox.go's tests. They live under inbox_assemble_test.go rather
// than inbox_test.go, which is spec 020's /api/v1/inbox HTTP suite in
// package api_test.

// inboxFact builds an active-state store.ProjectWorkFact for the inbox
// assembly tests: identity, project, state, the two ownership fields §3.2
// classifies on, and blocker ids. Priority is medium and the age is a fixed
// 3h everywhere, so nothing but fan-out separates the roots in the §3.3
// ordering fixture.
func inboxFact(id, project, state, assignee, creator string, blockers ...string) store.ProjectWorkFact {
	f := store.ProjectWorkFact{
		Task: model.Task{
			ID: id, Project: project, Title: id + " title", Priority: "medium",
			State: state, Assignee: assignee, CreatedBy: creator,
			CreatedAt: fixedNow.Add(-3 * time.Hour),
		},
		StateEvent: &store.EventFact{At: fixedNow.Add(-3 * time.Hour)},
	}
	for _, b := range blockers {
		f.OpenBlockers = append(f.OpenBlockers, ref(b, "ready"))
	}
	return f
}

// inboxReviewFor builds an open pr-kind approval age old. required is the
// approval's required_actor (nil for an unassigned review).
func inboxReviewFor(id int64, project, author string, required *string, age time.Duration) store.InboxReview {
	return store.InboxReview{
		ApprovalID: id, Project: project, EntityID: "org/repo#" + strconv.FormatInt(id, 10),
		Title:       "PR " + strconv.FormatInt(id, 10),
		URL:         "https://example.test/pr/" + strconv.FormatInt(id, 10),
		AuthorLogin: author, RequiredActor: required,
		CreatedAt: fixedNow.Add(-age),
	}
}

func actorRef(s string) *string { return &s }

// TestAssembleInbox pins spec 056 §3.1-§3.3: which item lands in which
// bucket, the fixed bucket order and headings, and the order within a
// bucket. Each case names the buckets it expects, in order, with each
// bucket's item hrefs in order.
func TestAssembleInbox(t *testing.T) {
	t.Parallel()

	type wantBucket struct {
		label string
		hrefs []string
	}
	cases := []struct {
		name string
		in   inboxInputs
		want []wantBucket
	}{
		{
			// All six §3.2 buckets, for a lead of project alpha.
			name: "six buckets in order",
			in: inboxInputs{
				ActorID: "ada", ActorLogin: "ada-gh", Now: fixedNow,
				Membership: map[string]bool{"alpha": true},
				Led:        map[string]bool{"alpha": true},
				Reviews: []store.InboxReview{
					inboxReviewFor(1, "alpha", "zed-gh", actorRef("ada"), time.Hour),
					inboxReviewFor(2, "alpha", "zed-gh", nil, time.Hour),
					inboxReviewFor(3, "alpha", "ada-gh", actorRef("bob"), time.Hour),
				},
				Facts: []store.ProjectWorkFact{
					inboxFact("ALPHA-10", "alpha", "in_progress", "ada", "zed"),
					inboxFact("ALPHA-11", "alpha", "ready", "", "ada"),
					inboxFact("ALPHA-12", "alpha", "ready", "zed", "zed"),
					// Out: a merged task is not active, and gamma is not a
					// project the actor is a member of.
					inboxFact("ALPHA-13", "alpha", "merged", "zed", "zed"),
					inboxFact("GAMMA-1", "gamma", "ready", "zed", "zed"),
				},
			},
			want: []wantBucket{
				{"Reviews assigned to you", []string{"https://example.test/pr/1"}},
				{"Unassigned reviews in projects you lead", []string{"https://example.test/pr/2"}},
				{"Reviews you own", []string{"https://example.test/pr/3"}},
				{"Work assigned to you", []string{"/tasks/ALPHA-10"}},
				{"Work you own", []string{"/tasks/ALPHA-11"}},
				{"Other in-progress work", []string{"/tasks/ALPHA-12"}},
			},
		},
		{
			// Bucket 2 belongs to leads only: bob is a plain member of
			// alpha, so the unassigned review never reaches him.
			name: "non-lead member never sees the unassigned bucket",
			in: inboxInputs{
				ActorID: "bob", ActorLogin: "bob-gh", Now: fixedNow,
				Membership: map[string]bool{"alpha": true},
				Led:        map[string]bool{},
				Reviews: []store.InboxReview{
					inboxReviewFor(2, "alpha", "zed-gh", nil, time.Hour),
				},
				Facts: []store.ProjectWorkFact{
					inboxFact("ALPHA-12", "alpha", "ready", "zed", "zed"),
				},
			},
			want: []wantBucket{
				{"Other in-progress work", []string{"/tasks/ALPHA-12"}},
			},
		},
		{
			// §3.3: score over every project, filter to membership after.
			// ALPHA-1 and ALPHA-3 are held by BETA-9 (fan-out 4), ALPHA-2 by
			// BETA-7 (fan-out 2), and both roots live in beta — a project
			// the actor is not a member of. Filtering the facts first would
			// truncate each chain at its direct blocker, making BETA-1,
			// BETA-3 and BETA-4 the roots and ordering the bucket
			// ALPHA-2, ALPHA-1, ALPHA-3 by root id.
			name: "work order follows the cross-project root cause",
			in: inboxInputs{
				ActorID: "ada", ActorLogin: "ada-gh", Now: fixedNow,
				Membership: map[string]bool{"alpha": true},
				Led:        map[string]bool{},
				Facts: []store.ProjectWorkFact{
					inboxFact("ALPHA-1", "alpha", "ready", "zed", "zed", "BETA-3"),
					inboxFact("ALPHA-2", "alpha", "ready", "zed", "zed", "BETA-1"),
					inboxFact("ALPHA-3", "alpha", "ready", "zed", "zed", "BETA-4"),
					inboxFact("BETA-1", "beta", "ready", "zed", "zed", "BETA-7"),
					inboxFact("BETA-3", "beta", "ready", "zed", "zed", "BETA-9"),
					inboxFact("BETA-4", "beta", "ready", "zed", "zed", "BETA-9"),
					inboxFact("BETA-7", "beta", "ready", "zed", "zed"),
					inboxFact("BETA-9", "beta", "ready", "zed", "zed"),
				},
			},
			want: []wantBucket{
				{"Other in-progress work", []string{"/tasks/ALPHA-1", "/tasks/ALPHA-3", "/tasks/ALPHA-2"}},
			},
		},
		{
			// Review buckets order oldest-open first, ties broken by
			// approval id. Input order is deliberately none of that.
			name: "reviews oldest first with id tiebreak",
			in: inboxInputs{
				ActorID: "ada", ActorLogin: "ada-gh", Now: fixedNow,
				Membership: map[string]bool{"alpha": true},
				Led:        map[string]bool{},
				Reviews: []store.InboxReview{
					inboxReviewFor(2, "alpha", "zed-gh", actorRef("ada"), 5*time.Hour),
					inboxReviewFor(3, "alpha", "zed-gh", actorRef("ada"), time.Hour),
					inboxReviewFor(1, "alpha", "zed-gh", actorRef("ada"), 5*time.Hour),
				},
			},
			want: []wantBucket{
				{"Reviews assigned to you", []string{
					"https://example.test/pr/1", "https://example.test/pr/2", "https://example.test/pr/3",
				}},
			},
		},
		{
			// Authorship matches case-insensitively; an empty author never
			// matches, and an empty actor login never matches an author.
			name: "authorship is case-insensitive and never empty-matches",
			in: inboxInputs{
				ActorID: "ada", ActorLogin: "Ada-GH", Now: fixedNow,
				Membership: map[string]bool{"alpha": true},
				Led:        map[string]bool{},
				Reviews: []store.InboxReview{
					inboxReviewFor(1, "alpha", "aDa-gh", actorRef("bob"), time.Hour),
					inboxReviewFor(2, "alpha", "zed-gh", actorRef("bob"), time.Hour),
					inboxReviewFor(3, "alpha", "", actorRef("bob"), time.Hour),
				},
			},
			want: []wantBucket{
				{"Reviews you own", []string{"https://example.test/pr/1"}},
			},
		},
		{
			name: "empty actor login matches no author",
			in: inboxInputs{
				ActorID: "ada", ActorLogin: "", Now: fixedNow,
				Membership: map[string]bool{"alpha": true},
				Led:        map[string]bool{},
				Reviews: []store.InboxReview{
					inboxReviewFor(3, "alpha", "", actorRef("bob"), time.Hour),
				},
			},
			want: nil,
		},
		{
			name: "nothing waiting",
			in: inboxInputs{
				ActorID: "ada", ActorLogin: "ada-gh", Now: fixedNow,
				Membership: map[string]bool{"alpha": true},
				Led:        map[string]bool{"alpha": true},
				Facts: []store.ProjectWorkFact{
					inboxFact("GAMMA-1", "gamma", "ready", "zed", "zed"),
					inboxFact("ALPHA-13", "alpha", "merged", "zed", "zed"),
				},
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := assembleInbox(tc.in)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("assembleInbox = %#v, want nil (every bucket empty)", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("assembleInbox = nil, want %d buckets", len(tc.want))
			}
			if len(got.Buckets) != len(tc.want) {
				t.Fatalf("buckets = %v, want %d", bucketLabels(got.Buckets), len(tc.want))
			}
			for i, w := range tc.want {
				b := got.Buckets[i]
				if b.Label != w.label {
					t.Errorf("bucket %d label = %q, want %q", i, b.Label, w.label)
				}
				var hrefs []string
				for _, it := range b.Items {
					hrefs = append(hrefs, it.Href)
				}
				if len(hrefs) != len(w.hrefs) {
					t.Fatalf("bucket %q items = %v, want %v", w.label, hrefs, w.hrefs)
				}
				for j := range hrefs {
					if hrefs[j] != w.hrefs[j] {
						t.Errorf("bucket %q items = %v, want %v", w.label, hrefs, w.hrefs)
						break
					}
				}
			}
		})
	}
}

func bucketLabels(bs []ui.InboxBucket) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.Label)
	}
	return out
}

// TestAssembleInboxItemText pins what an item renders as: the text the row
// shows and the muted detail (project, age).
func TestAssembleInboxItemText(t *testing.T) {
	t.Parallel()
	got := assembleInbox(inboxInputs{
		ActorID: "ada", ActorLogin: "ada-gh", Now: fixedNow,
		Membership: map[string]bool{"alpha": true},
		Led:        map[string]bool{},
		Reviews: []store.InboxReview{
			inboxReviewFor(1, "alpha", "zed-gh", actorRef("ada"), 26*time.Hour),
		},
		Facts: []store.ProjectWorkFact{
			inboxFact("ALPHA-10", "alpha", "in_progress", "ada", "zed"),
		},
	})
	if got == nil || len(got.Buckets) != 2 {
		t.Fatalf("assembleInbox = %#v, want a review and a work bucket", got)
	}
	review := got.Buckets[0].Items[0]
	if review.Text != "PR 1" || review.Detail != "alpha · 1 day" {
		t.Errorf("review item = %+v, want text %q detail %q", review, "PR 1", "alpha · 1 day")
	}
	work := got.Buckets[1].Items[0]
	if work.Text != "ALPHA-10 ALPHA-10 title" || work.Detail != "alpha · 3 hours" {
		t.Errorf("work item = %+v", work)
	}
}
