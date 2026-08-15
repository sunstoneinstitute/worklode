// render.go maps internal/model values (model.BoardResponse,
// model.CockpitProjection, timeline entries, ...) into the presentation view
// types internal/ui's templ components render. This is the one-way seam that
// keeps the dependency pointing api -> ui: ui never sees an api type, api
// never imports ui's templates' internals. The web page handlers in web.go
// build a ui view through these funcs and call ui.<Page>(view).Render.
package api

import (
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// boardView maps the org-wide board (shared with GET /api/v1/board) plus the
// untriaged-inbox count into the ui board view. isHome selects the Home
// heading over the Work heading; active/title drive the shell.
func boardView(b *model.BoardResponse, inboxCount int, isHome bool, title, active string) ui.BoardView {
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
			InProgress: p.InProgress,
			InReview:   p.InReview,
			Ready:      p.Ready,
			Blocked:    p.Blocked,
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

// projectsView maps the cross-project portfolio, dropping store.Project's
// curated cockpit-only fields (ADR 036 §3) down to the model.Project shape
// the page actually renders (id, name, key).
func projectsView(projects []store.Project, title, active string) ui.ProjectsView {
	out := make([]model.Project, 0, len(projects))
	for _, p := range projects {
		out = append(out, model.Project{ID: p.ID, Name: p.Name, Key: p.Key, Focus: p.Focus})
	}
	return ui.ProjectsView{
		Page:     ui.PageProps{Title: title, ActiveGlobal: active},
		Projects: out,
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
func placeholderProjectView(c *model.CockpitProjection, heading, message, section string) ui.PlaceholderView {
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
func cockpitView(c *model.CockpitProjection, title string) ui.CockpitView {
	return ui.CockpitView{
		Page:         ui.PageProps{Title: title},
		CanonicalURL: c.CanonicalURL,
		NewTaskURL:   "/projects/" + c.Project.ID + "/tasks/new",
		Project:      cockpitProject(c.Project),
		ModeName:     c.Mode.Name,
		ModeBasis:    c.Mode.Basis.Summary,
		PinnedFocus:  cockpitFocus(c.PinnedFocus),
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

// deliverablesView maps a project's declared deliverables into the project's
// Deliverables page. No row carries a state, because none is stored (spec 029
// §3.2) — the page says so once rather than per row.
func deliverablesView(project ui.CockpitProject, items []model.Deliverable) ui.DeliverablesView {
	v := ui.DeliverablesView{
		Page:         ui.PageProps{Title: "worklode: " + project.Name + ": Deliverables"},
		CanonicalURL: "/projects/" + project.ID + "/deliverables",
		Project:      project,
		NewURL:       "/projects/" + project.ID + "/deliverables/new",
		Deliverables: make([]ui.DeliverableRow, 0, len(items)),
	}
	for _, d := range items {
		v.Deliverables = append(v.Deliverables, ui.DeliverableRow{
			ID:          d.ID,
			Name:        d.Name,
			Description: d.Description,
			URL:         d.URL,
			CreatedBy:   d.CreatedBy,
			CreatedAt:   d.CreatedAt,
		})
	}
	return v
}

// newTaskView builds the new-task form, with the submitted values selected in
// the menus and errMsg shown ("" on first render).
func newTaskView(project ui.CockpitProject, v taskFormValues, errMsg string) ui.NewTaskView {
	return ui.NewTaskView{
		Form: ui.FormShell{
			Page:      ui.PageProps{Title: "worklode: " + project.Name + ": new task"},
			Project:   project,
			Action:    "/projects/" + project.ID + "/tasks",
			CancelURL: "/projects/" + project.ID,
			Error:     errMsg,
		},
		Title:      v.Title,
		Body:       v.Body,
		Priorities: formOptions(webTaskPriorities, v.Priority, ""),
		Kinds:      formOptions(webTaskKinds, v.Kind, ""),
		Concerns:   formOptions(webTaskConcerns, v.Concern, "None"),
		Draft:      v.Draft,
	}
}

// newDeliverableView builds the deliverable form the same way.
func newDeliverableView(project ui.CockpitProject, v deliverableFormValues, errMsg string) ui.NewDeliverableView {
	return ui.NewDeliverableView{
		Form: ui.FormShell{
			Page:      ui.PageProps{Title: "worklode: " + project.Name + ": new deliverable"},
			Project:   project,
			Action:    "/projects/" + project.ID + "/deliverables",
			CancelURL: "/projects/" + project.ID + "/deliverables",
			Error:     errMsg,
		},
		Name:        v.Name,
		Description: v.Description,
		URL:         v.URL,
	}
}

// formOptions renders a menu from a fixed value list, marking the selected
// one. A non-empty emptyLabel prepends an empty-valued choice (the optional
// concern's "None"), selected when nothing is chosen.
func formOptions(values []string, selected, emptyLabel string) []ui.FormOption {
	out := make([]ui.FormOption, 0, len(values)+1)
	if emptyLabel != "" {
		out = append(out, ui.FormOption{Value: "", Label: emptyLabel, Selected: selected == ""})
	}
	for _, value := range values {
		out = append(out, ui.FormOption{
			Value:    value,
			Label:    formOptionLabel(value),
			Selected: value == selected,
		})
	}
	return out
}

// formOptionLabel capitalizes a menu value for display ("critical" ->
// "Critical"), leaving the submitted value itself untouched.
func formOptionLabel(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

// cockpitProject maps the project identity, reused by the cockpit and the
// project placeholder sidebar.
func cockpitProject(p model.CockpitProject) ui.CockpitProject {
	return ui.CockpitProject{ID: p.ID, Name: p.Name, Key: p.Key}
}

// cockpitFocus maps the pinned-focus note, preserving nil (nothing pinned).
// The pinner's display name flattens to "" when the actor is unknown.
func cockpitFocus(f *model.Focus) *ui.CockpitFocus {
	if f == nil {
		return nil
	}
	cf := &ui.CockpitFocus{Note: f.Note, PinnedAt: f.PinnedAt}
	if f.PinnedBy != nil {
		cf.PinnedBy = f.PinnedBy.Name
	}
	return cf
}

// cockpitDecision maps the next governed decision, preserving nil (no decision
// ready).
func cockpitDecision(d *model.Decision) *ui.CockpitDecision {
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
func workRows(items []model.CockpitWorkItem) []ui.WorkRow {
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
func cockpitConcerns(items []model.SecondaryConcern) []ui.CockpitConcern {
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
func cockpitRepos(items []model.Repository) []ui.CockpitRepo {
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
func cockpitCostTotals(c model.ProjectCost) []ui.CockpitCostTotal {
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
