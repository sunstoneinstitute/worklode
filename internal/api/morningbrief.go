package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// morningBriefEventCap bounds one brief render.
// ponytail: flat cap, oldest-first; an actor away long enough to accrue
// more sees a truncation line and reviews forward in steps. Upgrade path:
// summarize-then-page, only if real briefs ever hit the cap.
const morningBriefEventCap = 2000

// briefEventsSince pages ListEvents (After cursor) until a short page, the
// cap, or the horizon. Ascending id order, horizon-bounded by ListEvents
// itself. truncated reports hitting the cap with more behind it.
func (s *server) briefEventsSince(ctx context.Context, after int64) (events []store.Event, truncated bool, err error) {
	cursor := after
	for len(events) < morningBriefEventCap {
		limit := morningBriefEventCap - len(events)
		if limit > store.MaxEventListLimit {
			limit = store.MaxEventListLimit
		}
		page, err := s.st.ListEvents(ctx, store.EventFilter{After: cursor, Limit: limit})
		if err != nil {
			return nil, false, err
		}
		if len(page) == 0 {
			return events, false, nil
		}
		events = append(events, page...)
		cursor = page[len(page)-1].ID
		if len(page) < limit {
			return events, false, nil // short page: reached the horizon
		}
	}
	// Hit the cap exactly (the last page's limit was shrunk to fit it). One
	// more page, asking for a single row, is enough to say whether anything
	// sits behind the cap without pulling it all in.
	more, err := s.st.ListEvents(ctx, store.EventFilter{After: cursor, Limit: 1})
	if err != nil {
		return nil, false, err
	}
	return events, len(more) > 0, nil
}

// morningBriefTier is 032 §9's ordering, from the spec's own numbered list.
// Tier 1 (decisions and exceptions needing the actor) is never assigned to
// an event: it is derived from open state in assembleMorningBrief, which is
// what makes it persist across cursor advances.
type morningBriefTier int

const (
	briefTierOutcome morningBriefTier = 2 // material outcomes and changes
	briefTierStopped morningBriefTier = 3 // runs that stopped or reached a bound
	briefTierRoutine morningBriefTier = 4 // routine successful work, collapsed
)

// morningBriefTierOf classifies one event type per the pinned table in the
// plan's Global Constraints. Unknown types are routine — the default that
// keeps a new producer from paging a human by accident.
func morningBriefTierOf(eventType string) morningBriefTier {
	switch eventType {
	case "task.stopped", "task.abandoned", "lease.expired",
		"runtime.crashloop", "runtime.oom", "runtime.flux_failure":
		return briefTierStopped
	case "wl:DocumentSubmitted", "wl:DocumentAccepted", "task.done",
		"task.reopened", "deliverable.created", "approval.decided",
		"crew.member_added", "crew.member_removed", "issue.promoted",
		"runtime.flux_recovery":
		return briefTierOutcome
	default:
		return briefTierRoutine
	}
}

// morningBriefProject attributes one event to a project: payload "project",
// else the "<KEY>-" prefix of payload "task" via keyToProject, else the
// payload's repository.full_name via repoToProject. "" means unattributed —
// the caller drops the event from the brief.
func morningBriefProject(ev store.Event, keyToProject, repoToProject map[string]string) string {
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		return ""
	}

	if project, ok := payload["project"].(string); ok && project != "" {
		return project
	}

	if taskID, ok := payload["task"].(string); ok {
		if i := strings.IndexByte(taskID, '-'); i >= 0 {
			if project, ok := keyToProject[taskID[:i]]; ok {
				return project
			}
		}
	}

	if repository, ok := payload["repository"].(map[string]any); ok {
		if fullName, ok := repository["full_name"].(string); ok {
			if project, ok := repoToProject[fullName]; ok {
				return project
			}
		}
	}

	return ""
}

// morningBriefInputs is everything the brief derivation reads, already
// fetched. Events are ascending by id, all above Boundary; Truncated means
// the fetch hit its cap and Events is a prefix. Order is the project-id
// display order (the Home cards' order); Awaiting and Assigned are the
// tier-1 open state, keyed by project id, and are independent of Events.
type morningBriefInputs struct {
	Events        []store.Event
	Truncated     bool
	Boundary      int64 // the stored cursor (0 = never reviewed)
	Order         []string
	Projects      map[string]store.Project // by id, for Name and FocusNote
	Awaiting      map[string]int           // ApprovalsAwaiting counts
	Assigned      map[string][]store.OwnedWork
	KeyToProject  map[string]string
	RepoToProject map[string]string
}

// assembleMorningBrief derives the brief. nil when there is nothing at all:
// no tier-1 state anywhere and no events past the boundary — the section
// then does not render. Groups appear in Order; a project with neither
// tier-1 state nor attributed events gets no group. Within a group: tier 1
// from Awaiting/Assigned, tiers 2 and 3 from events in id order, tier 4 as
// a count. Cutoff is the highest event id seen (Boundary when Events is
// empty); CanReview is Cutoff > Boundary.
func assembleMorningBrief(in morningBriefInputs) *ui.MorningBriefView {
	hasState := false
	for _, n := range in.Awaiting {
		if n > 0 {
			hasState = true
			break
		}
	}
	if !hasState {
		for _, work := range in.Assigned {
			if len(work) > 0 {
				hasState = true
				break
			}
		}
	}
	if !hasState && len(in.Events) == 0 {
		return nil
	}

	cutoff := in.Boundary
	if n := len(in.Events); n > 0 {
		cutoff = in.Events[n-1].ID
	}

	inOrder := make(map[string]bool, len(in.Order))
	for _, pid := range in.Order {
		inOrder[pid] = true
	}

	groups := make(map[string]*ui.MorningBriefGroup, len(in.Order))
	group := func(pid string) *ui.MorningBriefGroup {
		g, ok := groups[pid]
		if !ok {
			proj := in.Projects[pid]
			g = &ui.MorningBriefGroup{ProjectID: pid, Name: proj.Name, FocusNote: proj.FocusNote}
			groups[pid] = g
		}
		return g
	}

	for _, pid := range in.Order {
		if n := in.Awaiting[pid]; n > 0 {
			group(pid).NeedsYou = append(group(pid).NeedsYou, ui.MorningBriefItem{
				Text: morningBriefAwaitingText(n),
				Href: "/reviews",
			})
		}
		for _, w := range in.Assigned[pid] {
			group(pid).NeedsYou = append(group(pid).NeedsYou, ui.MorningBriefItem{
				Text: "Assigned to you: " + w.ID + " " + w.Title,
				Href: "/tasks/" + w.ID,
			})
		}
	}

	for _, ev := range in.Events {
		pid := morningBriefProject(ev, in.KeyToProject, in.RepoToProject)
		if pid == "" || !inOrder[pid] {
			continue
		}
		g := group(pid)
		switch morningBriefTierOf(ev.Type) {
		case briefTierOutcome:
			text, href := morningBriefItemText(ev)
			g.Outcomes = append(g.Outcomes, ui.MorningBriefItem{Text: text, Href: href})
		case briefTierStopped:
			text, href := morningBriefItemText(ev)
			g.Stopped = append(g.Stopped, ui.MorningBriefItem{Text: text, Href: href})
		default:
			g.Routine++
		}
	}

	view := &ui.MorningBriefView{
		Cutoff:    cutoff,
		CanReview: cutoff > in.Boundary,
		Truncated: in.Truncated,
		Shown:     len(in.Events),
	}
	for _, pid := range in.Order {
		g, ok := groups[pid]
		if !ok {
			continue
		}
		if len(g.NeedsYou) == 0 && len(g.Outcomes) == 0 && len(g.Stopped) == 0 && g.Routine == 0 {
			continue
		}
		view.Groups = append(view.Groups, *g)
	}
	return view
}

// morningBriefAwaitingText spells the tier-1 approvals-awaiting item, using
// the exact singular/plural wording pinned by the Global Constraints (see
// homeSignal in home.go for the same spelling on the Home card).
func morningBriefAwaitingText(n int) string {
	if n == 1 {
		return "1 approval awaiting you"
	}
	return fmt.Sprintf("%d approvals awaiting you", n)
}

// morningBriefItemText renders one event as a brief line: a `task` payload
// key yields "<type>: <task id>" linking to the task; approval.decided
// links to Reviews; anything else is the bare type label with no link.
// Deliberately dumb — the acceptance bar is legibility, not payload
// archaeology.
func morningBriefItemText(ev store.Event) (text, href string) {
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err == nil {
		if taskID, ok := payload["task"].(string); ok && taskID != "" {
			return ev.Type + ": " + taskID, "/tasks/" + taskID
		}
	}
	if ev.Type == "approval.decided" {
		return ev.Type, "/reviews"
	}
	return ev.Type, ""
}
