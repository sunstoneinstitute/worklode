// home.go is the pure projection half of Home's card assembly: which
// projects get a card and with what facts, given everything Home's page
// handler has already fetched. It decides no ordering, no tier, and no
// display strings — that is the next layer's job (rendering into
// ui.HomeCard). It reads no store and no request; assembleHomeFacts is a
// plain function over already-fetched inputs so it can be tested without a
// database.
package api

import (
	"fmt"
	"sort"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
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

const maxCrewInitials = 5

// homeTier ranks a card for actor-mode sort order: 1 = approvals awaiting
// the actor, 2 = the actor leads the project, 3 = the actor is on the
// project. Open mode has no tiers; callers must not call this when
// homeInputs.OpenMode is true.
func homeTier(f homeCardFacts) int {
	switch {
	case f.Awaiting > 0:
		return 1
	case f.IsLead:
		return 2
	default:
		return 3
	}
}

// homeSignal is the card's one-line "why this card is here" (exact
// spellings pinned in constraints.md); "" in open mode, which never calls
// this.
func homeSignal(f homeCardFacts) string {
	switch {
	case f.Awaiting == 1:
		return "1 approval awaiting you"
	case f.Awaiting > 1:
		return fmt.Sprintf("%d approvals awaiting you", f.Awaiting)
	case f.IsLead:
		return "You lead this project"
	case f.IsMember:
		return "You are on this project"
	default:
		return ""
	}
}

// crewInitials maps up to maxCrewInitials display names (lead-first, as
// assembled) to ui.Initials, plus the count truncated beyond that.
func crewInitials(names []string) ([]string, int) {
	if len(names) <= maxCrewInitials {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = ui.Initials(n)
		}
		return out, 0
	}

	out := make([]string, maxCrewInitials)
	for i := 0; i < maxCrewInitials; i++ {
		out[i] = ui.Initials(names[i])
	}
	return out, len(names) - maxCrewInitials
}

// homeCards derives tier and signal from assembleHomeFacts's projection,
// maps facts to ui.HomeCard, and sorts: actor mode by (tier ascending, last
// activity descending, project ID ascending); open mode by (last activity
// descending, project ID ascending) with no tiers and every Signal/RoleBadge
// left "".
func homeCards(in homeInputs) []ui.HomeCard {
	facts := assembleHomeFacts(in)

	if in.OpenMode {
		sort.SliceStable(facts, func(i, j int) bool {
			if !facts[i].LastActivity.Equal(facts[j].LastActivity) {
				return facts[i].LastActivity.After(facts[j].LastActivity)
			}
			return facts[i].Project.ID < facts[j].Project.ID
		})
	} else {
		sort.SliceStable(facts, func(i, j int) bool {
			ti, tj := homeTier(facts[i]), homeTier(facts[j])
			if ti != tj {
				return ti < tj
			}
			if !facts[i].LastActivity.Equal(facts[j].LastActivity) {
				return facts[i].LastActivity.After(facts[j].LastActivity)
			}
			return facts[i].Project.ID < facts[j].Project.ID
		})
	}

	cards := make([]ui.HomeCard, len(facts))
	for i, f := range facts {
		var signal, badge string
		if !in.OpenMode {
			signal = homeSignal(f)
			switch {
			case f.IsLead:
				badge = "Lead"
			case f.IsMember:
				badge = "Member"
			}
		}

		crew, more := crewInitials(f.CrewNames)

		cards[i] = ui.HomeCard{
			ProjectID:    f.Project.ID,
			Name:         f.Project.Name,
			Key:          f.Project.Key,
			RoleBadge:    badge,
			Signal:       signal,
			InProgress:   f.InProgress,
			InReview:     f.InReview,
			Blocked:      f.Blocked,
			CrewInitials: crew,
			CrewMore:     more,
			LastActivity: f.LastActivity,
		}
	}

	return cards
}
