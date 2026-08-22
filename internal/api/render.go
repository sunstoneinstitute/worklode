// render.go maps internal/model values (model.BoardResponse,
// model.CockpitProjection, timeline entries, ...) into the presentation view
// types internal/ui's templ components render. This is the one-way seam that
// keeps the dependency pointing api -> ui: ui never sees an api type, api
// never imports ui's templates' internals. The web page handlers in web.go
// build a ui view through these funcs and call ui.<Page>(view).Render.
package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/mdrender"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// boardView maps the org-wide board (shared with GET /api/v1/board) plus the
// untriaged-inbox count into the ui board view. active/title drive the shell.
func boardView(b *model.BoardResponse, inboxCount int, title, active string) ui.BoardView {
	v := ui.BoardView{
		Page:       ui.PageProps{Title: title, ActiveGlobal: active},
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

// driftView maps spec 007's four read surfaces into the drift board's view.
// drift and gaps are nil when no graph-server is configured; graphEnabled is
// what tells the page the difference between "no findings" and "nothing
// looked", so it is passed rather than inferred from the nil slices.
func driftView(frontier []model.FrontierTask, cp *model.CriticalPath, drift *model.Drift, gaps []model.Gap, graphEnabled bool) ui.DriftView {
	v := ui.DriftView{
		Page:         ui.PageProps{Title: "worklode: drift", ActiveGlobal: "knowledge"},
		Frontier:     frontier,
		Gaps:         gaps,
		GraphEnabled: graphEnabled,
	}
	if cp != nil {
		v.CriticalPath = *cp
	}
	if drift != nil {
		v.Drift = *drift
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

// approvalsView maps the awaiting-approvals queue (029 §7.1) into the
// Reviews page's view type; now is the reference point FmtAge renders each
// row's age against. ID carries through because each row renders the decide
// form that posts to /approvals/{id}/decide (029 §7.3).
func approvalsView(rows []store.AwaitingApproval, now time.Time) ui.ApprovalsView {
	out := make([]ui.ApprovalRow, 0, len(rows))
	for _, a := range rows {
		row := ui.ApprovalRow{
			ID:          a.ID,
			EntityID:    a.EntityID,
			PRTitle:     a.PRTitle,
			PRURL:       a.PRURL,
			TaskID:      a.TaskID,
			ProjectID:   a.ProjectID,
			ProjectName: a.ProjectName,
			Age:         ui.FmtAge(a.CreatedAt, now),
		}
		if a.RequiredActorName != nil {
			row.RequiredActorName = *a.RequiredActorName
		}
		out = append(out, row)
	}
	return ui.ApprovalsView{
		Page: ui.PageProps{Title: "worklode: reviews", ActiveGlobal: "reviews"},
		Rows: out,
	}
}

// timelineRows maps a task's timeline entries into rendered rows via
// summarizeEntry (which stays in api: internal/ui takes pre-formatted rows,
// not the raw entries).
func timelineRows(entries []model.TimelineEntry) []ui.TimelineRow {
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
// It drops the projection's RankingFocus and Repositories: mode B renders no
// ranking-focus list and no repositories panel, and mapping a fact no markup
// reads only makes the view look richer than the page is (WL-164). Both stay
// on the JSON cockpit, which is where they are contracted.
func cockpitView(c *model.CockpitProjection, title string) ui.CockpitView {
	return ui.CockpitView{
		Page:         ui.PageProps{Title: title},
		CanonicalURL: c.CanonicalURL,
		NewTaskURL:   "/projects/" + c.Project.ID + "/tasks/new",
		Project:      cockpitProject(c.Project),
		ModeName:     c.Mode.Name,
		ModeBasis:    c.Mode.Basis.Summary,
		PinnedFocus:  cockpitFocus(c.PinnedFocus),
		NextDecision: cockpitDecision(c.NextDecision),
		Work: ui.CockpitWork{
			InProgress: workRows(c.Work.InProgress),
			InReview:   workRows(c.Work.InReview),
			Ready:      workRows(c.Work.Ready),
			Blocked:    workRows(c.Work.Blocked),
		},
		SecondaryConcerns: cockpitConcerns(c.SecondaryConcerns),
		CostTotals:        cockpitCostTotals(c.Cost),
	}
}

// deliverablesView maps a project's declared deliverables into the project's
// Deliverables page. A row's state is not stored (spec 029 §3.2): it is the
// newest evidence reported against the declared address, carried on the read
// projection, and empty until an emitter reports one.
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
			ID:            d.ID,
			Name:          d.Name,
			Description:   d.Description,
			URL:           d.URL,
			CreatedBy:     d.CreatedBy,
			CreatedAt:     d.CreatedAt,
			Artifact:      d.Artifact,
			ReportedState: d.ReportedState,
			ReportedAt:    d.ReportedAt,
		})
	}
	return v
}

// docsView maps the document corpus into the /docs index.
func docsView(docs []model.Doc) ui.DocsView {
	v := ui.DocsView{
		Page: ui.PageProps{Title: "worklode: documents"},
		Docs: make([]ui.DocRow, 0, len(docs)),
	}
	// Bodies are dropped for the same reason the JSON list drops them: the
	// index renders none of the markdown, and carrying every document's
	// source into the page would make it the heaviest response the cockpit
	// serves.
	for _, d := range withoutDocBodies(docs) {
		v.Docs = append(v.Docs, ui.DocRow{Doc: d, URL: docPageURL(d.ID), Ref: docRef(d)})
	}
	return v
}

// docView maps one document's detail projection into its page.
//
// md may be nil, which renders every body afresh; see the mdcache field.
func docView(md *mdrender.Cache, d *model.DocDetail) ui.DocView {
	return ui.DocView{
		Page: ui.PageProps{Title: "worklode: " + d.Slug},
		Doc:  d.Doc,
		// Rendered here rather than in ui for the reason taskView gives.
		// DocBody rather than Body: a document's {#sec-N} headings are
		// addressable anchors, and the Sections table links at them.
		BodyHTML: md.DocBody(d.Doc.Body),
		Ref:      docRef(d.Doc),
		Sections: d.Sections,
		Edges:    docEdgeRows(d.Edges),
		EdgesIn:  docEdgeRows(d.EdgesIn),
		Revision: d.Revision,
	}
}

// docEdgeRows renders each edge's far end: a link labelled with the other
// document's slug and corpus reference — the store resolved both alongside
// the id — or the verbatim reference when the edge names something outside
// this backbone. The id is the last resort, for a row whose join found no
// document to name.
func docEdgeRows(edges []model.DocEdge) []ui.DocEdgeRow {
	out := make([]ui.DocEdgeRow, 0, len(edges))
	for _, e := range edges {
		row := ui.DocEdgeRow{Type: e.Type, Anchor: e.FromAnchor, Label: e.ToExternal}
		if e.ToDoc != 0 {
			row.URL = docPageURL(e.ToDoc)
			row.Label = e.ToSlug
			if row.Label == "" {
				row.Label = "document " + strconv.FormatInt(e.ToDoc, 10)
			}
			row.Ref = docEdgeRef(e)
			if e.ToAnchor != "" {
				row.Label += "#" + e.ToAnchor
			}
		}
		out = append(out, row)
	}
	return out
}

// docEdgeRef is the far end's corpus reference for display, in docRef's
// spelling: "spec 25", or the kind alone for a plan.
func docEdgeRef(e model.DocEdge) string {
	if e.ToNumber == 0 {
		return e.ToKind
	}
	return e.ToKind + " " + strconv.Itoa(e.ToNumber)
}

// docRef is a document's corpus reference for display: "spec 25", or the kind
// alone for a plan, which carries no number (025 §14.3).
func docRef(d model.Doc) string {
	if d.Number == 0 {
		return d.Kind
	}
	return d.Kind + " " + strconv.Itoa(d.Number)
}

// docPageURL is a document's cockpit page path.
func docPageURL(id int64) string { return "/docs/" + strconv.FormatInt(id, 10) }

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

// cockpitConcerns maps the secondary concerns (what holds a ready task).
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

// cockpitCostTotals maps the per-currency cost totals for the cockpit's cost
// window.
func cockpitCostTotals(c model.CostReport) []ui.CockpitCostTotal {
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

// taskView maps one task, its edges and its timeline into the task page's
// view. The edge loops classify by type rather than by direction, so an
// outgoing child_of names the parent while an incoming one names a child.
//
// md may be nil, which renders every body afresh; see the mdcache field.
func taskView(md *mdrender.Cache, t *model.Task, blocked bool, entries []model.TimelineEntry, out, in []store.Edge) ui.TaskView {
	view := ui.TaskView{
		Page: ui.PageProps{Title: "worklode: " + t.ID},
		Task: *t,
		// Sanitising happens here rather than in ui: internal/ui is a
		// stdlib + internal/model leaf and cannot import mdrender's
		// goldmark/bluemonday dependencies (ADR 036 §3, CLAUDE.md).
		BodyHTML: md.Body(t.Body),
		Blocked:  blocked,
		Timeline: timelineRows(entries),
	}
	for _, e := range out {
		switch e.Type {
		case "blocks":
			view.Blocks = append(view.Blocks, e.ToTask)
		case "child_of":
			view.Parent = e.ToTask
		case "follow_up_to":
			view.FollowUpTo = e.ToTask
		}
	}
	for _, e := range in {
		switch e.Type {
		case "blocks":
			view.BlockedBy = append(view.BlockedBy, e.FromTask)
		case "child_of":
			view.Children = append(view.Children, e.FromTask)
		case "follow_up_to":
			view.FollowUps = append(view.FollowUps, e.FromTask)
		}
	}
	return view
}
