// render.go maps internal/api's read-model DTOs (boardResponse,
// cockpitProjection, timeline entries, ...) into the presentation view types
// internal/ui's templ components render. This is the one-way seam that keeps
// the dependency pointing api -> ui: ui never sees an api type, api never
// imports ui's templates' internals. The web page handlers in web.go build a
// ui view through these funcs and call ui.<Page>(view).Render.
package api

import (
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// boardView maps the org-wide board (shared with GET /api/v1/board) plus the
// untriaged-inbox count into the ui board view. isHome selects the Home
// heading over the Work heading; active/title drive the shell.
func boardView(b *boardResponse, inboxCount int, isHome bool, title, active string) ui.BoardView {
	v := ui.BoardView{
		Page:       ui.PageProps{Title: title, ActiveGlobal: active},
		IsHome:     isHome,
		InboxCount: inboxCount,
		Projects:   make([]ui.BoardProject, 0, len(b.Projects)),
	}
	for _, p := range b.Projects {
		v.Projects = append(v.Projects, ui.BoardProject{
			ID:         p.ID,
			Name:       p.Name,
			InProgress: boardItems(p.InProgress),
			InReview:   boardItems(p.InReview),
			Ready:      boardItems(p.Ready),
			Blocked:    boardItems(p.Blocked),
		})
	}
	for _, f := range b.RecentFailures {
		v.RecentFailures = append(v.RecentFailures, ui.BoardFailure{
			OccurredAt: f.OccurredAt,
			Cluster:    f.Cluster,
			Kind:       f.Kind,
			Workload:   f.Workload,
			Message:    f.Message,
		})
	}
	return v
}

// boardItems maps one board bucket's rows, carrying the lease holder through
// as a nil-able BoardHolder.
func boardItems(items []boardTaskJSON) []ui.BoardItem {
	out := make([]ui.BoardItem, 0, len(items))
	for _, it := range items {
		bi := ui.BoardItem{
			ID:       it.ID,
			Title:    it.Title,
			Priority: it.Priority,
			State:    it.State,
			Assignee: it.Assignee,
		}
		if it.Holder != nil {
			bi.Holder = &ui.BoardHolder{ActorID: it.Holder.ActorID, ExpiresAt: it.Holder.ExpiresAt}
		}
		out = append(out, bi)
	}
	return out
}

// projectsView maps the cross-project portfolio. store.Project rows pass
// through unchanged (ui may import store).
func projectsView(projects []store.Project, title, active string) ui.ProjectsView {
	return ui.ProjectsView{
		Page:     ui.PageProps{Title: title, ActiveGlobal: active},
		Projects: projects,
	}
}

// timelineRows maps a task's timeline entries into rendered rows via
// summarizeEntry (which stays in api because it reads api's timelineEntry).
func timelineRows(entries []timelineEntry) []ui.TimelineRow {
	out := make([]ui.TimelineRow, 0, len(entries))
	for _, e := range entries {
		out = append(out, summarizeEntry(e))
	}
	return out
}

// placeholderGlobalView builds the honest placeholder for a global
// destination (no project sidebar).
func placeholderGlobalView(destination, heading, message string) ui.PlaceholderView {
	return ui.PlaceholderView{
		Page:    ui.PageProps{Title: "worklode: " + heading, ActiveGlobal: destination},
		Heading: heading,
		Message: message,
	}
}

// placeholderProjectView builds the honest placeholder for a not-yet-built
// project section, carrying the project identity and active section so the
// page renders the same sidebar as the overview page.
func placeholderProjectView(c *cockpitProjection, heading, message, section string) ui.PlaceholderView {
	proj := cockpitProject(c.Project)
	return ui.PlaceholderView{
		Page:          ui.PageProps{Title: "worklode: " + c.Project.Name + ": " + heading},
		Heading:       heading,
		Message:       message,
		Project:       &proj,
		CanonicalURL:  c.CanonicalURL,
		ActiveSection: section,
	}
}

// cockpitView maps the project cockpit projection into the ui overview view.
func cockpitView(c *cockpitProjection, title string) ui.CockpitView {
	return ui.CockpitView{
		Page:         ui.PageProps{Title: title},
		CanonicalURL: c.CanonicalURL,
		Project:      cockpitProject(c.Project),
		ModeName:     string(c.Mode.Name),
		ModeBasis:    c.Mode.Basis.Summary,
		RankingFocus: c.RankingFocus,
		NextDecision: cockpitDecision(c.NextDecision),
		Work: ui.CockpitWork{
			InProgress: workRows(c.Work.InProgress),
			InReview:   workRows(c.Work.InReview),
			Ready:      workRows(c.Work.Ready),
			Blocked:    workRows(c.Work.Blocked),
		},
		SecondaryConcerns: cockpitConcerns(c.SecondaryConcerns),
		Repositories:      cockpitRepos(c.Repositories),
		CostTotals:        cockpitCostTotals(c.Cost),
	}
}

// cockpitProject maps the project identity, reused by the cockpit and the
// project placeholder sidebar.
func cockpitProject(p cockpitProjectJSON) ui.CockpitProject {
	return ui.CockpitProject{ID: p.ID, Name: p.Name, Key: p.Key}
}

// cockpitDecision maps the next governed decision, preserving nil (no decision
// ready).
func cockpitDecision(d *decisionJSON) *ui.CockpitDecision {
	if d == nil {
		return nil
	}
	return &ui.CockpitDecision{
		Title:       d.Title,
		Accountable: d.Accountable,
		Readiness:   d.Readiness,
	}
}

// workRows maps one cockpit work bucket, flattening owner/delegate to their
// display names ("" when absent).
func workRows(items []cockpitWorkItem) []ui.WorkRow {
	out := make([]ui.WorkRow, 0, len(items))
	for _, it := range items {
		wr := ui.WorkRow{
			ID:               it.ID,
			Title:            it.Title,
			State:            it.State,
			Priority:         it.Priority,
			URL:              it.URL,
			EvidenceCategory: it.StatusEvidence.Category,
			EvidenceSummary:  it.StatusEvidence.Summary,
		}
		if it.Owner != nil {
			wr.Owner = it.Owner.Name
		}
		if it.Delegate != nil {
			wr.Delegate = it.Delegate.Name
		}
		out = append(out, wr)
	}
	return out
}

// cockpitConcerns maps the secondary concerns (open blockers).
func cockpitConcerns(items []secondaryConcernJSON) []ui.CockpitConcern {
	out := make([]ui.CockpitConcern, 0, len(items))
	for _, c := range items {
		out = append(out, ui.CockpitConcern{
			Title:           c.Title,
			URL:             c.URL,
			EvidenceSummary: c.Evidence.Summary,
		})
	}
	return out
}

// cockpitRepos maps the mapped repositories and their declared done-state
// evidence.
func cockpitRepos(items []repositoryJSON) []ui.CockpitRepo {
	out := make([]ui.CockpitRepo, 0, len(items))
	for _, r := range items {
		out = append(out, ui.CockpitRepo{
			Repo:             r.Repo,
			DoneState:        r.DoneState,
			EvidenceCategory: r.StatusEvidence.Category,
		})
	}
	return out
}

// cockpitCostTotals maps the per-currency cost totals for the cockpit's cost
// window.
func cockpitCostTotals(c projectCostJSON) []ui.CockpitCostTotal {
	out := make([]ui.CockpitCostTotal, 0, len(c.Totals))
	for _, t := range c.Totals {
		out = append(out, ui.CockpitCostTotal{
			Currency:       t.Currency,
			CostAmount:     t.CostAmount,
			UnpricedTokens: t.UnpricedTokens,
		})
	}
	return out
}
