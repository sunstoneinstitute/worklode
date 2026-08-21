// home.go is the pure projection half of Home's card assembly: which
// projects get a card and with what facts, given everything Home's page
// handler has already fetched. It decides no ordering, no tier, and no
// display strings — that is the next layer's job (rendering into
// ui.HomeCard). It reads no store and no request; assembleHomeFacts is a
// plain function over already-fetched inputs so it can be tested without a
// database.
package api

import (
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// homeInputs is everything Home's card assembly reads, already fetched.
// Membership and Awaiting are ignored entirely in open mode — the maps may
// be empty, but the projection never trusts that; it only ever reads them
// when OpenMode is false. Field shapes follow the landed store rulings in
// constraints.md: Membership keys by project id from ProjectsForActor,
// Awaiting keys by project id from ApprovalsAwaiting, Participants is
// display names (lead first) built by the caller from ListParticipants.
type homeInputs struct {
	Projects     []store.Project                    // every project, in the order to preserve
	Facts        map[string][]store.ProjectWorkFact // by project id
	Membership   map[string]memberFacts             // from ProjectsForActor, actor mode only
	Awaiting     map[string]int                     // from ApprovalsAwaiting, actor mode only
	Participants map[string][]string                // display names, lead first, by project id
	OpenMode     bool
}

// memberFacts is the viewer's relationship to one project.
type memberFacts struct{ IsLead bool }

// homeCardFacts is one project's assembled facts, before tiering, ordering,
// or display-string mapping.
type homeCardFacts struct {
	Project                       store.Project
	IsMember, IsLead              bool
	Awaiting                      int
	InProgress, InReview, Blocked int
	CrewNames                     []string
	LastActivity                  time.Time
}

// assembleHomeFacts projects homeInputs onto cards-to-be.
//
// Actor mode (OpenMode false): a project gets a card if the actor is a
// member of it (present in Membership) or has approvals awaiting them on it
// (Awaiting > 0) — a card can exist for a non-member, and then it carries no
// role. A project that is neither is left off entirely; an actor on no
// projects with nothing awaiting them gets a nil/empty slice, never a
// fabricated card.
//
// Open mode (OpenMode true): every project gets a card. Membership and
// Awaiting are never consulted — even if the caller populated them, the
// projection ignores them — so IsMember, IsLead, and Awaiting are always
// false/false/0.
//
// Counts and activity come only through bucketWorkFacts and lastActivity
// (the one reader for each fact family); CrewNames is copied verbatim from
// Participants. Output preserves the input Projects order; sorting into
// tiers is the next layer's job.
func assembleHomeFacts(in homeInputs) []homeCardFacts {
	var cards []homeCardFacts

	for _, p := range in.Projects {
		var mf memberFacts
		var isMember bool
		var awaiting int

		if !in.OpenMode {
			mf, isMember = in.Membership[p.ID]
			awaiting = in.Awaiting[p.ID]

			if !isMember && awaiting <= 0 {
				continue
			}
		}

		facts := in.Facts[p.ID]
		buckets := bucketWorkFacts(facts)

		cards = append(cards, homeCardFacts{
			Project:      p,
			IsMember:     isMember,
			IsLead:       isMember && mf.IsLead,
			Awaiting:     awaiting,
			InProgress:   len(buckets.InProgress),
			InReview:     len(buckets.InReview),
			Blocked:      len(buckets.Blocked),
			CrewNames:    in.Participants[p.ID],
			LastActivity: lastActivity(facts),
		})
	}

	return cards
}
