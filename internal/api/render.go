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

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
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
//
// The kind label and the revision are formatted here, not in internal/ui,
// which takes pre-formatted rows. Only a document shows its revision: an
// approval is granted against one version and the reviewer needs to see
// which, where a PR's own page already shows the head its link resolves to.
func approvalsView(rows []store.AwaitingApproval, now time.Time) ui.ApprovalsView {
	out := make([]ui.ApprovalRow, 0, len(rows))
	for _, a := range rows {
		row := ui.ApprovalRow{
			ID:          a.ID,
			Kind:        a.EntityKind,
			EntityID:    a.EntityID,
			Title:       a.Title,
			URL:         a.URL,
			TaskID:      a.Task,
			ProjectID:   a.Project,
			ProjectName: a.ProjectName,
			Age:         ui.FmtAge(a.CreatedAt, now),
		}
		switch a.EntityKind {
		case "pr":
			row.Kind = "PR"
		case "doc":
			row.Kind = "Document"
			row.Revision = a.SubjectRevision
		}
		if a.RequiredActorName != nil {
			row.RequiredActorName = *a.RequiredActorName
		}
		out = append(out, row)
	}
	return ui.ApprovalsView{
		Page: ui.PageProps{Title: "worklode: reviews"},
		Rows: out,
	}
}

// agentSessionRows maps a task's agent sessions into rendered rows. The times
// are formatted here, not in internal/ui: ui takes pre-formatted rows and has
// no clock, and FmtAge is the phrasing every relative age on the cockpit
// already uses (ui.FmtAge).
func agentSessionRows(sessions []model.AgentSession, now time.Time) []ui.AgentSessionRow {
	out := make([]ui.AgentSessionRow, 0, len(sessions))
	for _, a := range sessions {
		out = append(out, agentSessionRow(a, now))
	}
	return out
}

// agentSessionRow maps one session. The opaque session id is deliberately not
// carried: it addresses a row in the API and no page renders it, and a view
// field nothing reads is how a page grows fields that quietly stop being true.
func agentSessionRow(a model.AgentSession, now time.Time) ui.AgentSessionRow {
	return ui.AgentSessionRow{
		Agent:        a.Agent,
		AgentVersion: a.AgentVersion,
		Started:      ui.FmtAge(a.StartedAt, now),
		LastSeen:     ui.FmtAge(a.LastSeenAt, now),
		Running:      a.EndedAt == nil,
	}
}

// projectAgentSessionRows is agentSessionRows for the cockpit, where a session
// has to name the task it is on — the project page lists work from every task,
// so a row without one places nothing.
func projectAgentSessionRows(sessions []store.ProjectAgentSession, now time.Time) []ui.AgentSessionRow {
	out := make([]ui.AgentSessionRow, 0, len(sessions))
	for _, p := range sessions {
		row := agentSessionRow(p.AgentSession, now)
		row.ActorID = p.ActorID
		row.Task = p.TaskID
		row.TaskTitle = p.TaskTitle
		row.TaskURL = "/tasks/" + p.TaskID
		out = append(out, row)
	}
	return out
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
		Project:      cockpitProjectInMode(c.Project, c.Mode),
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

// rallyCardView maps an active rally task, its direct member count
// (RallyMemberCount) and its open-blocker tree (BlockerTree) into the
// cockpit's rally card (WL-667). tree's depth-1 nodes are exactly those same
// members still open, so they become Members and Done falls out as total
// minus len(Members). Two rules keep that subtraction non-negative, and both
// are enforced: a rally never carries a plan_doc (planMintableKinds excludes
// rally, and checkKindRetag refuses a retag of a task that has one), so
// BlockerTree's plan-blocking branch contributes nothing; and both readers
// skip tombstoned tasks.
func rallyCardView(rally *model.Task, total int, tree model.BlockerTree) ui.CockpitRally {
	var open []ui.CockpitRallyMember
	for _, b := range tree.Blockers {
		if b.Depth != 1 {
			continue
		}
		open = append(open, ui.CockpitRallyMember{ID: b.ID, Title: b.Title, URL: "/tasks/" + b.ID})
	}
	return ui.CockpitRally{
		ID:      rally.ID,
		Title:   rally.Title,
		URL:     "/tasks/" + rally.ID,
		Done:    total - len(open),
		Total:   total,
		Members: open,
	}
}

// deliverablesView maps a project's declared deliverables into the project's
// Deliverables page, grouped by milestone (spec 029 §2): one group per
// milestone in position order — only milestones that actually hold a
// deliverable — then an unattached group last. A row's state is not stored
// (spec 029 §3.2): it is the newest evidence reported against the declared
// address, carried on the read projection, and empty until an emitter
// reports one.
func deliverablesView(project ui.CockpitProject, items []model.Deliverable, milestones []model.Milestone) ui.DeliverablesView {
	v := ui.DeliverablesView{
		Page:         ui.PageProps{Title: "worklode: " + project.Name + ": Deliverables"},
		CanonicalURL: "/projects/" + project.ID + "/deliverables",
		Project:      project,
		NewURL:       "/projects/" + project.ID + "/deliverables/new",
	}

	byMilestone := make(map[string][]ui.DeliverableRow, len(milestones))
	var unattached []ui.DeliverableRow
	for _, d := range items {
		row := ui.DeliverableRow{
			ID:            d.ID,
			Name:          d.Name,
			Description:   d.Description,
			URL:           d.URL,
			CreatedBy:     d.CreatedBy,
			CreatedAt:     d.CreatedAt,
			Artifact:      d.Artifact,
			ReportedState: d.ReportedState,
			ReportedAt:    d.ReportedAt,
		}
		if d.Milestone == "" {
			unattached = append(unattached, row)
		} else {
			byMilestone[d.Milestone] = append(byMilestone[d.Milestone], row)
		}
	}
	for _, m := range milestones {
		if rows := byMilestone[m.ID]; len(rows) > 0 {
			v.Groups = append(v.Groups, ui.DeliverableGroup{
				MilestoneID: m.ID, MilestoneTitle: m.Title, Rows: rows,
			})
		}
	}
	// The unattached group is always last, and always present unless at
	// least one milestone group already carries the page's only content: a
	// project with no milestones (or none holding a deliverable) then shows
	// exactly one group, which the template renders headerless — today's
	// flat page.
	if len(unattached) > 0 || len(v.Groups) == 0 {
		v.Groups = append(v.Groups, ui.DeliverableGroup{Rows: unattached})
	}
	return v
}

// docsView maps the document corpus into the /docs index.
func docsView(docs []model.Doc, projectKeys map[string]string) ui.DocsView {
	v := ui.DocsView{
		Page: ui.PageProps{Title: "worklode: documents", ActiveGlobal: "knowledge"},
		Docs: make([]ui.DocRow, 0, len(docs)),
	}
	// Bodies are dropped for the same reason the JSON list drops them: the
	// index renders none of the markdown, and carrying every document's
	// source into the page would make it the heaviest response the cockpit
	// serves.
	for _, d := range withoutDocBodies(docs) {
		ref := docRef(d)
		if d.Number != 0 {
			ref = docWebRef(d, projectKeys[d.Project])
		}
		url := docCanonicalURL(d, projectKeys[d.Project])
		v.Docs = append(v.Docs, ui.DocRow{Doc: d, URL: url, Ref: ref})
	}
	return v
}

// docView maps one document's detail projection into its page.
//
// md may be nil, which renders every body afresh; see the mdcache field.
func docView(md *mdrender.Cache, keys mdrender.ProjectKeys, d *model.DocDetail) ui.DocView {
	return ui.DocView{
		Page: ui.PageProps{Title: "worklode: " + d.Slug, ActiveGlobal: "knowledge"},
		Doc:  d.Doc,
		// Rendered here rather than in ui for the reason taskView gives.
		// DocBody rather than Body: a document's {#sec-N} headings are
		// addressable anchors, and the Sections table links at them.
		BodyHTML: md.DocBody(keys, d.Doc.Body),
		Ref:      docRef(d.Doc),
		Sections: d.Sections,
		Edges:    docEdgeRows(d.Edges),
		EdgesIn:  docEdgeRows(d.EdgesIn),
		Revision: d.Revision,
	}
}

// docVersionView maps one document version into its page (GET
// /docs/versions/{id}/{n}). doc carries the document's live identity —
// status, project, corpus reference; projectKey is that project's key,
// needed only to build DocURL's shorthand form when doc carries a number.
// ver is the version rendered, current when its number matches doc's live
// version.
func docVersionView(md *mdrender.Cache, keys mdrender.ProjectKeys, doc model.Doc, ver model.DocVersion, projectKey string) ui.DocVersionView {
	docURL := docCanonicalURL(doc, projectKey)
	return ui.DocVersionView{
		Page:     ui.PageProps{Title: "worklode: " + doc.Slug + " v" + strconv.Itoa(ver.Version), ActiveGlobal: "knowledge"},
		Doc:      doc,
		Ref:      docRef(doc),
		Version:  ver,
		BodyHTML: md.DocBody(keys, ver.Body),
		Current:  ver.Version == doc.Version,
		DocURL:   docURL,
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
		switch {
		case e.ToDoc != 0:
			row.Label = e.ToSlug
			if row.Label == "" {
				row.Label = "document " + strconv.FormatInt(e.ToDoc, 10)
			} else if e.ToNumber == 0 {
				row.URL = docPageURL(e.ToDoc)
			} else {
				row.URL = "/docs/ref/" + e.ToSlug
			}
			row.Ref = docEdgeRef(e)
			if e.ToAnchor != "" {
				// The fragment rides the link, not just the label (WL-301).
				row.URL += "#" + e.ToAnchor
				row.Label += "#" + e.ToAnchor
			}
		case e.ToExternal != "":
			// A reference the store kept verbatim — a corpus filename like
			// "004-execution-backbone.md#sec-5.1" — still resolves at read
			// time through the reference redirect (WL-301): the href's own
			// #fragment survives the 302. A ref outside the grammar keeps
			// URL "" and renders as text.
			base, frag := designdoc.SplitFragment(e.ToExternal)
			if _, ok := designdoc.ParseNumberForm(base); ok || designdoc.LooksLikePath(base) {
				row.URL = "/docs/ref/" + base
				if frag != "" {
					row.URL += "#" + frag
				}
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
// alone for a document with no number — which since 029 §4's backfill means one
// created before it, plans included.
func docRef(d model.Doc) string {
	if d.Number == 0 {
		return d.Kind
	}
	return d.Kind + " " + strconv.Itoa(d.Number)
}

// docCanonicalURL is the one spelling of a document's cockpit URL: the
// cross-corpus shorthand ("/docs/WL-SPEC-25") when the document carries a
// number, else the numeric page URL. The docs index, the version page, and
// the /docs/ref/ redirect all link through it — they must agree or links
// 404, so the rule lives here alone (WL-347).
func docCanonicalURL(d model.Doc, projectKey string) string {
	if d.Number != 0 {
		return "/docs/" + docWebRef(d, projectKey)
	}
	return docPageURL(d.ID)
}

// docWebRef is a document's direct cockpit reference: the cross-corpus
// shorthand, which every kind now carries (029 §4).
func docWebRef(d model.Doc, projectKey string) string {
	return projectKey + "-" + strings.ToUpper(d.Kind) + "-" + strconv.Itoa(d.Number)
}

// docPageURL is the fallback cockpit page path for a document with no number
// to build a shorthand from: a tombstone, whose slug may be reused, and any row
// predating 029 §4's backfill. Plans used to need it and no longer do.
func docPageURL(id int64) string { return "/docs/" + strconv.FormatInt(id, 10) }

// formTitle is a creation form's document title, prefixed "Error: " when the
// submit was rejected. On a rejected submit the browser announces the new
// document's title before anything on the page, so the prefix is what tells a
// screen-reader user the submit failed; the message itself takes focus
// separately (see forms.templ).
func formTitle(base, errMsg string) string {
	if errMsg == "" {
		return base
	}
	return "Error: " + base
}

// newTaskView builds the new-task form, with the submitted values selected in
// the menus and errMsg shown ("" on first render).
func newTaskView(project ui.CockpitProject, v taskFormValues, errMsg string, dictation bool) ui.NewTaskView {
	return ui.NewTaskView{
		Form: ui.FormShell{
			Page:      ui.PageProps{Title: formTitle("worklode: "+project.Name+": new task", errMsg)},
			Project:   project,
			Action:    "/projects/" + project.ID + "/tasks",
			CancelURL: "/projects/" + project.ID,
			Error:     errMsg,
			Dictation: dictation,
		},
		Title:      v.Title,
		Body:       v.Body,
		Priorities: formOptions(webTaskPriorities, v.Priority, ""),
		Kinds:      formOptions(webTaskKinds, v.Kind, ""),
		Concerns:   formOptions(webTaskConcerns, v.Concern, "None"),
		Draft:      v.Draft,
	}
}

// newDeliverableView builds the deliverable form the same way, offering the
// project's milestones as the optional attach-at-declaration choice
// (spec 029 §2), default "No milestone".
func newDeliverableView(project ui.CockpitProject, v deliverableFormValues, milestones []model.Milestone, errMsg string, dictation bool) ui.NewDeliverableView {
	return ui.NewDeliverableView{
		Form: ui.FormShell{
			Page:      ui.PageProps{Title: formTitle("worklode: "+project.Name+": new deliverable", errMsg)},
			Project:   project,
			Action:    "/projects/" + project.ID + "/deliverables",
			CancelURL: "/projects/" + project.ID + "/deliverables",
			Error:     errMsg,
			Dictation: dictation,
		},
		Name:        v.Name,
		Description: v.Description,
		URL:         v.URL,
		Artifact:    v.Artifact,
		Milestones:  milestoneFormOptions(milestones, v.Milestone),
	}
}

// milestoneFormOptions renders a project's milestones as a menu, with a
// leading "No milestone" choice selected when nothing was chosen — the
// deliverable form's milestone select shares this with no other caller
// because milestones are IDs and titles, not a fixed value list formOptions
// covers.
func milestoneFormOptions(milestones []model.Milestone, selected string) []ui.FormOption {
	out := make([]ui.FormOption, 0, len(milestones)+1)
	out = append(out, ui.FormOption{Value: "", Label: "No milestone", Selected: selected == ""})
	for _, m := range milestones {
		out = append(out, ui.FormOption{Value: m.ID, Label: m.Title, Selected: m.ID == selected})
	}
	return out
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
// project placeholder sidebar. It leaves the mode empty, so those pages'
// sidebars render no mode pill.
func cockpitProject(p model.CockpitProject) ui.CockpitProject {
	return ui.CockpitProject{ID: p.ID, Name: p.Name, Key: p.Key}
}

// cockpitProjectInMode is cockpitProject plus the operating mode, which only
// the cockpit knows and only its sidebar renders.
func cockpitProjectInMode(p model.CockpitProject, m model.CockpitMode) ui.CockpitProject {
	v := cockpitProject(p)
	v.ModeName = m.Name
	v.ModeBasis = m.Basis.Summary
	return v
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
func cockpitDecision(d *model.CockpitDecision) *ui.CockpitDecision {
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
			Currency:           t.Currency,
			CostAmount:         t.CostAmount,
			UnpricedTokens:     t.UnpricedTokens,
			OverheadCostAmount: t.Overhead.CostAmount,
		})
	}
	return out
}

// taskView maps one task, its edges and its timeline into the task page's
// view. The edge loops classify by type rather than by direction, so an
// outgoing child_of names the parent while an incoming one names a child.
//
// md may be nil, which renders every body afresh; see the mdcache field.
func taskView(md *mdrender.Cache, keys mdrender.ProjectKeys, t *model.Task, project ui.CockpitProject, blocked bool, entries []model.TimelineEntry, out, in []store.Edge) ui.TaskView {
	view := ui.TaskView{
		Page:    ui.PageProps{Title: "worklode: " + t.ID},
		Project: project,
		Task:    *t,
		// Sanitising happens here rather than in ui: internal/ui is a
		// stdlib + internal/model leaf and cannot import mdrender's
		// goldmark/bluemonday dependencies (ADR 036 §3, CLAUDE.md).
		BodyHTML: md.Body(keys, t.Body),
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
		case "duplicate_of":
			view.DuplicateOf = e.ToTask
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
		case "duplicate_of":
			view.Duplicates = append(view.Duplicates, e.FromTask)
		}
	}
	return view
}
