// render.go maps internal/model values (model.BoardResponse,
// model.CockpitProjection, timeline entries, ...) into the presentation view
// types internal/ui's templ components render. This is the one-way seam that
// keeps the dependency pointing api -> ui: ui never sees an api type, api
// never imports ui's templates' internals. The web page handlers in web.go
// build a ui view through these funcs and call ui.<Page>(view).Render.
package api

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/mdrender"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// driftView maps WL-SPEC-82's four read surfaces into the drift board's view.
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

// approvalsView maps the awaiting-approvals queue (WL-REQ-124) into the
// Reviews page's view type; now is the reference point FmtAge renders each
// row's age against. ID carries through because each row renders the decide
// form that posts to /approvals/{id}/decide (WL-REQ-124).
//
// Rows split into three groups, in this order, each keeping the store's
// oldest-first order (WL-1003):
//
//  1. Waiting on the viewer: required_actor is actorID, required_role is one
//     of groups (the Home badge's match, store.ApprovalsAwaiting), or the
//     viewer authored it under a flow that allows self-review of its kind
//     (selfReview, from store.SelfReviewableApprovals).
//  2. Assigned to nobody: neither required_actor nor required_role is set.
//  3. Assigned to someone else. These stay listed: the store does not limit
//     a decision to required_actor, and hiding them would hide work again.
//
// The revision is formatted here, not in internal/ui, which takes
// pre-formatted rows. Every kind but a PR shows its revision: an approval is
// granted against one version and the reviewer needs to see which, where a
// PR's own page already shows the head its link resolves to.
//
// Decidable follows the store's own rule: an approval naming no revision
// cannot be decided, so the row must not offer the form.
func approvalsView(rows []store.AwaitingApproval, now time.Time,
	actorID string, groups []string, selfReview map[int64]bool) ui.ApprovalsView {
	var mine, nobody, others []store.AwaitingApproval
	for _, a := range rows {
		switch {
		case actorID != "" && a.RequiredActor != nil && *a.RequiredActor == actorID,
			a.RequiredRole != nil && slices.Contains(groups, *a.RequiredRole),
			selfReview[a.ID]:
			mine = append(mine, a)
		case a.RequiredActor == nil && a.RequiredRole == nil:
			nobody = append(nobody, a)
		default:
			others = append(others, a)
		}
	}
	v := ui.ApprovalsView{Page: ui.PageProps{Title: "worklode: reviews"}}
	for _, g := range []struct {
		heading string
		rows    []store.AwaitingApproval
	}{
		{"Waiting on you", mine},
		{"Assigned to nobody", nobody},
		{"Assigned to someone else", others},
	} {
		if len(g.rows) > 0 {
			v.Groups = append(v.Groups, ui.ApprovalGroup{Heading: g.heading, Rows: approvalRows(g.rows, now)})
		}
	}
	return v
}

// approvalRows maps queue rows for whichever page renders them: the Reviews
// queue, or one document's own review panel.
func approvalRows(rows []store.AwaitingApproval, now time.Time) []ui.ApprovalRow {
	out := make([]ui.ApprovalRow, 0, len(rows))
	for _, a := range rows {
		row := ui.ApprovalRow{
			ID:          a.ID,
			Kind:        a.EntityKind,
			EntityID:    a.EntityID,
			Title:       a.Title,
			URL:         a.URL,
			Lane:        a.Lane,
			TaskID:      a.Task,
			ProjectID:   a.Project,
			ProjectName: a.ProjectName,
			Age:         ui.FmtAge(a.CreatedAt, now),
			Decidable:   a.SubjectRevision != "",
		}
		if a.EntityKind != "pr" {
			row.Revision = a.SubjectRevision
		}
		if a.RequiredActorName != nil {
			row.RequiredActorName = *a.RequiredActorName
		}
		out = append(out, row)
	}
	return out
}

// approvalDetailView maps one approval plus its context into the detail
// page's view (WL-REQ-344): the entity it governs (title/url, resolved through
// EntityTitleURL — the exact join the Reviews queue uses, so the two pages
// cannot disagree about what an entity is called or where it opens), its
// full decision history (ListApprovalsForEntityCtx, newest first, this row
// included), and the governed references its own designation recorded
// (GovernedRefsForCtx). names resolves a resolving or authorizing actor id
// to a display name; an id absent from names (or nil on the row) renders as
// "" — honest empty, never a fabricated name.
//
// The compare link is offered only for a 'pr' row with a decided
// predecessor: the nearest older history row (this row's revision aside)
// whose state is 'approved' or 'rejected' — the same "decided" test
// DesignateRevision's own hasDecided check uses.
// policy is the project's self-review policy (store.SelfReviewPolicy): the
// flow name@rev the page names beside a self-reviewed decision.
func approvalDetailView(a *store.Approval, title, url string,
	history []store.Approval, governed []store.GovernedRef,
	names map[string]string, policy selfReviewPolicy) ui.ApprovalDetailView {
	v := ui.ApprovalDetailView{
		Page:      ui.PageProps{Title: "worklode: " + a.EntityKind + " " + a.EntityID},
		ID:        a.ID,
		Kind:      a.EntityKind,
		EntityID:  a.EntityID,
		Title:     title,
		URL:       url,
		Revision:  a.SubjectRevision,
		State:     a.State,
		CreatedAt: a.CreatedAt,
		DecidedAt: a.ResolvedAt,
	}
	if a.ResolvingActor != nil {
		v.DecidedBy = names[*a.ResolvingActor]
	}
	if a.Note != nil {
		v.Note = string(*a.Note)
	}
	v.ImpactOpen = a.ReviewKind == "impact" &&
		(a.State == "awaiting" || a.State == "changes_requested")
	if a.ExceptionAuthorizedBy != nil {
		v.ExceptionAuthorizedBy = names[*a.ExceptionAuthorizedBy]
		if policy.flowName != "" {
			v.ExceptionFlow = policy.flowName + "@" + policy.flowRev
		}
	}

	v.History = make([]ui.ApprovalHistoryRow, 0, len(history))
	var predecessor *store.Approval
	seenCurrent := false
	for i := range history {
		h := &history[i]
		row := ui.ApprovalHistoryRow{
			ID: h.ID, Revision: h.SubjectRevision, State: h.State,
			DecidedAt: h.ResolvedAt, Current: h.ID == a.ID,
		}
		if h.ResolvingActor != nil {
			row.DecidedBy = names[*h.ResolvingActor]
		}
		v.History = append(v.History, row)

		if h.ID == a.ID {
			seenCurrent = true
			continue
		}
		if seenCurrent && predecessor == nil && (h.State == "approved" || h.State == "rejected") {
			predecessor = h
		}
	}
	if a.EntityKind == "pr" && predecessor != nil {
		v.CompareURL = prCompareURL(url, predecessor.SubjectRevision, a.SubjectRevision)
	}

	v.Governed = make([]ui.GovernedRefRow, 0, len(governed))
	for _, g := range governed {
		v.Governed = append(v.Governed, ui.GovernedRefRow{Kind: g.Kind, ID: g.ID, Revision: g.Revision})
	}
	return v
}

// prCompareURL builds the GitHub diff-from-previous jump-out link for a
// 'pr'-kind approval with a decided predecessor (WL-REQ-124, WL-REQ-344): the
// compare view between the two head SHAs, derived from the stored PR URL
// (".../pull/<n>" -> ".../compare/<prev>...<current>"). "" when either
// revision is empty or prURL does not look like a GitHub PR URL — an honest
// empty rather than a broken link.
func prCompareURL(prURL, prev, current string) string {
	i := strings.Index(prURL, "/pull/")
	if i < 0 || prev == "" || current == "" {
		return ""
	}
	return prURL[:i] + "/compare/" + prev + "..." + current
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

// activityRows maps a task's activity log into rendered rows (WL-REQ-1237).
// Everything a row shows is derived here: internal/ui takes pre-formatted
// rows and reads no attribute of its own.
func activityRows(rows []model.TaskActivity) []ui.ActivityRow {
	out := make([]ui.ActivityRow, 0, len(rows))
	for _, a := range rows {
		out = append(out, activityRow(a))
	}
	return out
}

// activityRow maps one row. The claude_code. prefix is stripped because the
// card is already a coding agent's log and the prefix would be on every row.
func activityRow(a model.TaskActivity) ui.ActivityRow {
	return ui.ActivityRow{
		ID:      a.ID,
		At:      a.At,
		Event:   strings.TrimPrefix(a.Event, "claude_code."),
		Summary: activitySummary(a),
		Session: a.Session,
	}
}

// activitySummary is the one line a row shows beside its event name, built
// from the allowlisted attributes alone (WL-REQ-1237). The event name is the
// row's own cell, so no summary repeats it — except for a kind this switch
// does not model, where naming the event is the only honest thing to say. An
// attribute the exporter did not record contributes nothing rather than a
// zero: "ok" is a fact, and an absent success is not "failed".
func activitySummary(a model.TaskActivity) string {
	at := a.Attrs
	var parts []string
	add := func(s string) {
		if s != "" {
			parts = append(parts, s)
		}
	}
	switch a.Event {
	case "claude_code.tool_result":
		add(at.ToolName)
		if at.Success != nil {
			if *at.Success {
				add("ok")
			} else {
				add("failed")
			}
		}
		add(activityDuration(at.DurationMS))
		if at.Success != nil && !*at.Success {
			add(at.ErrorType)
		}
	case "claude_code.tool_decision":
		add(at.ToolName)
		add(at.DecisionType)
		add(at.DecisionSource)
	case "claude_code.api_request":
		add(at.Model)
		add(activityAttempt(at.Attempt))
	case "claude_code.api_error":
		if at.StatusCode > 0 {
			add(strconv.FormatInt(at.StatusCode, 10))
		}
		add(at.Error)
		add(activityAttempt(at.Attempt))
	case "claude_code.user_prompt":
		add(activityChars(at.PromptLength))
		add(at.CommandName)
	case "claude_code.assistant_response":
		add(at.Model)
		add(activityChars(at.ResponseLength))
	default:
		return a.Event
	}
	return strings.Join(parts, " ")
}

// activityDuration writes a tool call's duration at the precision a reader
// scanning the card can use: whole milliseconds under a second, one decimal
// of a second above it.
func activityDuration(ms int64) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 1000:
		return strconv.FormatInt(ms, 10) + "ms"
	default:
		return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) + "s"
	}
}

// activityAttempt names a retry. The first attempt is the ordinary case and
// saying so on every row would bury the retries worth noticing.
func activityAttempt(n int64) string {
	if n <= 1 {
		return ""
	}
	return "attempt " + strconv.FormatInt(n, 10)
}

func activityChars(n int64) string {
	if n <= 0 {
		return ""
	}
	return strconv.FormatInt(n, 10) + " chars"
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

// deliverablesView maps a project's declared deliverables into the project's
// Deliverables page, grouped by milestone (WL-REQ-120): one group per
// milestone in position order — only milestones that actually hold a
// deliverable — then an unattached group last. A row's state is not stored
// (WL-RULE-121): it is the newest evidence reported against the declared
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
			ID:                 d.ID,
			Name:               d.Name,
			Description:        string(d.Description),
			URL:                d.URL,
			CreatedBy:          d.CreatedBy,
			CreatedAt:          d.CreatedAt,
			Artifact:           d.Artifact,
			Label:              d.Label,
			ReportedState:      d.ReportedState,
			ReportedAt:         d.ReportedAt,
			ReportedProvenance: d.ReportedProvenance,
			ReportURL:          "/deliverables/" + d.ID + "/report",
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
				MilestoneID: m.ID, MilestoneTitle: string(m.Title), Rows: rows,
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
		url := model.DocCanonicalURL(d)
		v.Docs = append(v.Docs, ui.DocRow{Doc: d, URL: url, Ref: ref})
	}
	return v
}

// docView maps one document's detail projection into its page. body is the
// markdown to render — the consolidated fold (WL-REQ-192) or the stored source,
// which consolidated says which of — and path is this page's own URL, off
// which the two toggle spellings are built.
//
// md may be nil, which renders every body afresh; see the mdcache field.
func docView(md *mdrender.Cache, keys mdrender.ProjectKeys, d *model.DocDetail, body string, consolidated bool, path string) ui.DocView {
	keys = keys.InPage(arrangedAnchors(d.Sections))
	return ui.DocView{
		Page: ui.PageProps{Title: "worklode: " + cmp.Or(d.Ref, d.Slug), ActiveGlobal: "knowledge"},
		Doc:  d.Doc,
		// Rendered here rather than in ui for the reason taskView gives.
		// DocBody rather than Body: a document's {#sec-N} headings are
		// addressable anchors, and the Sections table links at them.
		BodyHTML:        md.DocBody(keys, body),
		Ref:             docRef(d.Doc),
		ProjectKey:      keys.Home(),
		Sections:        d.Sections,
		Edges:           docEdgeRows(d.Edges, keys),
		EdgesIn:         docEdgeRows(d.EdgesIn, keys),
		Revision:        d.Revision,
		Consolidated:    consolidated,
		ConsolidatedURL: path,
		SourceURL:       path + "?body=source",
		Notes:           docNoteRows(md, keys, d.Notes),
		Reviewers:       docReviewerRows(d.Doc),
	}
}

// arrangedAnchors maps each rule a document arranges to its section anchor,
// so the page links refs to those rules in place (WL-REQ-1633).
func arrangedAnchors(sections []model.DocSection) map[string]string {
	out := make(map[string]string)
	for _, s := range sections {
		if s.Rule != "" {
			out[s.Rule] = s.Anchor
		}
	}
	return out
}

// docNoteRows renders each anchored note's body as markdown — a note is
// prose an agent or a person wrote, and the corpus's other prose renders.
// Body rather than DocBody: a note carries no addressable sections, so it
// must not mint heading anchors that collide with the document's own.
func docNoteRows(md *mdrender.Cache, keys mdrender.ProjectKeys, notes []model.DocNote) []ui.DocNoteRow {
	out := make([]ui.DocNoteRow, 0, len(notes))
	for _, n := range notes {
		out = append(out, ui.DocNoteRow{
			Anchor:    n.Anchor,
			BodyHTML:  md.Body(keys, string(n.Body)),
			CreatedBy: n.CreatedBy,
			CreatedAt: n.CreatedAt,
			Task:      n.Task,
		})
	}
	return out
}

// docReviewerRows pairs the durable reviewer roster (WL-REQ-170) with who of it
// still owes a verdict on the current version. Both come from GetDoc, so a
// list row (which carries neither) renders no roster rather than an empty one.
func docReviewerRows(d model.Doc) []ui.DocReviewerRow {
	awaiting := make(map[string]bool, len(d.ReviewersAwaiting))
	for _, a := range d.ReviewersAwaiting {
		awaiting[a] = true
	}
	out := make([]ui.DocReviewerRow, 0, len(d.Reviewers))
	for _, r := range d.Reviewers {
		out = append(out, ui.DocReviewerRow{Actor: r, Awaiting: awaiting[r]})
	}
	return out
}

// docVersionView maps one document version into its page (GET
// /docs/versions/{id}/{n}). doc carries the document's live identity —
// status, project, corpus reference; projectKey is that project's key,
// needed only to build DocURL's shorthand form when doc carries a number.
// ver is the version rendered, current when its number matches doc's live
// version.
func docVersionView(md *mdrender.Cache, keys mdrender.ProjectKeys, doc model.Doc, ver model.DocVersion) ui.DocVersionView {
	docURL := model.DocCanonicalURL(doc)
	return ui.DocVersionView{
		Page:     ui.PageProps{Title: "worklode: " + cmp.Or(doc.Ref, doc.Slug) + " v" + strconv.Itoa(ver.Version), ActiveGlobal: "knowledge"},
		Doc:      doc,
		Ref:      docRef(doc),
		Version:  ver,
		BodyHTML: md.DocBody(keys, ver.Body),
		Current:  ver.Version == doc.Version,
		DocURL:   docURL,
	}
}

// docEdgeRows renders each edge's far end: a link to the other document's
// canonical page, labelled with its citable id and title ("WL-SPEC-73 System
// and deployment") — the store resolved both alongside the id — or the
// verbatim reference when the edge names something outside this backbone. The
// id is the last resort, for a row whose join found no document to name.
func docEdgeRows(edges []model.DocEdge, keys mdrender.ProjectKeys) []ui.DocEdgeRow {
	out := make([]ui.DocEdgeRow, 0, len(edges))
	for _, e := range edges {
		row := ui.DocEdgeRow{Type: e.Type, Anchor: e.FromAnchor, Label: e.ToExternal}
		switch {
		case e.ToDoc != 0:
			far := model.Doc{ID: e.ToDoc, Project: e.ToProject, ProjectKey: e.ToKey,
				Slug: e.ToSlug, Kind: e.ToKind, Number: e.ToNumber, Title: model.Markdown(e.ToTitle)}
			if far.Slug == "" {
				row.Label = "document " + strconv.FormatInt(far.ID, 10)
				break
			}
			row.URL = model.DocCanonicalURL(far)
			row.Label = far.FormatRef()
			if e.ToAnchor != "" {
				// The fragment rides the link, not just the label (WL-301).
				row.URL += "#" + e.ToAnchor
				row.Label += "#" + e.ToAnchor
			}
			if far.Title != "" {
				row.Label += " " + string(far.Title)
			}
		case e.ToExternal != "":
			// A reference the store kept verbatim. A bare number is a spec
			// on the referring project's sequence (WL-723), linked through the
			// root shortcut like an autolinked one; the href's #fragment
			// survives the redirect. A slug or corpus filename renders as text.
			base, frag := designdoc.SplitFragment(e.ToExternal)
			if nf, ok := designdoc.ParseNumberForm(base); ok && nf.Rest == "" && nf.Number > 0 && keys.Home() != "" {
				row.URL = "/" + keys.Home() + "-SPEC-" + strconv.Itoa(nf.Number)
				if frag != "" {
					row.URL += "#" + frag
				}
			}
		}
		out = append(out, row)
	}
	return out
}

// docRef is a document's corpus reference for display: "WL-SPEC-77", or the kind
// alone for a document with no number — which since WL-REQ-122's backfill means one
// created before it, plans included.
func docRef(d model.Doc) string {
	if d.Number == 0 {
		return d.Kind
	}
	return d.Kind + " " + strconv.Itoa(d.Number)
}

// docWebRef is a document's direct cockpit reference: the cross-corpus
// shorthand, which every kind now carries (WL-REQ-122).
func docWebRef(d model.Doc, projectKey string) string {
	return projectKey + "-" + strings.ToUpper(d.Kind) + "-" + strconv.Itoa(d.Number)
}

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

// newDeliverableView builds the deliverable form the same way, offering the
// project's milestones as the optional attach-at-declaration choice
// (WL-REQ-120), default "No milestone".
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
		Label:       v.Label,
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
		out = append(out, ui.FormOption{Value: m.ID, Label: string(m.Title), Selected: m.ID == selected})
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

// cockpitProject maps the project identity shown in the project sidebar.
func cockpitProject(p model.CockpitProject) ui.CockpitProject {
	return ui.CockpitProject{ID: p.ID, Name: p.Name, Key: p.Key}
}

// taskView maps one task, its edges and its timeline into the task page's
// view. The edge loops classify by type rather than by direction, so an
// outgoing childOf names the parent while an incoming one names a child.
//
// md may be nil, which renders every body afresh; see the mdcache field.
func taskView(md *mdrender.Cache, keys mdrender.ProjectKeys, t *model.Task, project ui.CockpitProject, blocked bool, entries []model.TimelineEntry, out, in []store.Edge) ui.TaskView {
	view := ui.TaskView{
		Page:    ui.PageProps{Title: "worklode: " + t.ID},
		Project: project,
		Task:    *t,
		// Sanitising happens here rather than in ui: internal/ui is a
		// stdlib + internal/model leaf and cannot import mdrender's
		// goldmark/bluemonday dependencies (WL-RULE-1349, CLAUDE.md).
		BodyHTML: md.Body(keys, string(t.Body)),
		Blocked:  blocked,
		Timeline: timelineRows(entries),
	}
	for _, e := range out {
		switch e.Type {
		case "blocks":
			view.Blocks = append(view.Blocks, e.ToTask)
		case "childOf":
			view.Parent = e.ToTask
		case "followUpTo":
			view.FollowUpTo = e.ToTask
		case "duplicateOf":
			view.DuplicateOf = e.ToTask
		}
	}
	for _, e := range in {
		switch e.Type {
		case "blocks":
			view.BlockedBy = append(view.BlockedBy, e.FromTask)
		case "childOf":
			view.Children = append(view.Children, e.FromTask)
		case "followUpTo":
			view.FollowUps = append(view.FollowUps, e.FromTask)
		case "duplicateOf":
			view.Duplicates = append(view.Duplicates, e.FromTask)
		}
	}
	return view
}

// taskPRRows renders a task's open pull requests for its header chip
// (WL-933). prs must already be filtered to State == "open" — merged and
// closed PRs stay in the timeline only. Label is "#<number>", prefixed with
// the PR's repo name when prs spans more than one repo, so a task worked
// across repos can still tell its PRs apart. ci is keyed the same way
// store.CIRunsForSHAs returns it; prCIState (runboard.go) rolls each PR's
// runs up to one state.
func taskPRRows(prs []store.PullRequest, ci map[store.RepoSHA][]store.CIRun) []ui.TaskPR {
	multiRepo := false
	for _, pr := range prs {
		if pr.Repo != prs[0].Repo {
			multiRepo = true
			break
		}
	}
	rows := make([]ui.TaskPR, 0, len(prs))
	for _, pr := range prs {
		label := fmt.Sprintf("#%d", pr.Number)
		if multiRepo {
			_, name, _ := strings.Cut(pr.Repo, "/")
			label = name + label
		}
		rows = append(rows, ui.TaskPR{
			Label:  label,
			URL:    pr.URL,
			CI:     prCIState(ci[store.RepoSHA{Repo: pr.Repo, SHA: pr.HeadSHA}]),
			Queued: pr.QueuedAt != nil,
		})
	}
	return rows
}

// prCIState rolls up every CI run recorded for a PR's head SHA into one
// state for the task page header dot (WL-933): take the newest run per
// workflow name, then any not yet "completed" makes the PR "running";
// otherwise any conclusion outside "success", "skipped" and "neutral" makes
// it "failed"; otherwise "passed". "" when no run is recorded.
func prCIState(runs []store.CIRun) string {
	newest := make(map[string]store.CIRun, len(runs))
	for _, r := range runs {
		if cur, ok := newest[r.Workflow]; !ok || r.StartedAt.After(cur.StartedAt) {
			newest[r.Workflow] = r
		}
	}
	if len(newest) == 0 {
		return ""
	}
	failed := false
	for _, r := range newest {
		if r.Status != "completed" {
			return "running"
		}
		conclusion := ""
		if r.Conclusion != nil {
			conclusion = *r.Conclusion
		}
		if conclusion != "success" && conclusion != "skipped" && conclusion != "neutral" {
			failed = true
		}
	}
	if failed {
		return "failed"
	}
	return "passed"
}

// ruleView maps one rule into its cockpit page (S20). projectID is the
// rule's owning project's id, off which CanonicalURL is built.
func ruleView(md *mdrender.Cache, keys mdrender.ProjectKeys, c *model.Rule, versions []model.RuleVersion, projectID string) ui.RuleView {
	v := ui.RuleView{
		Page:         ui.PageProps{Title: "worklode: " + c.Ref, ActiveGlobal: "knowledge"},
		Rule:         *c,
		BodyHTML:     md.Body(keys, string(c.Body)),
		Versions:     versions,
		CanonicalURL: fmt.Sprintf("/projects/%s/rule/%d", projectID, c.Number),
		// versions is newest first, so its head is the rule's live
		// version; an empty history is a read that failed, and the page
		// then makes no claim about which version this is.
		Current: len(versions) == 0 || c.Version == versions[0].Version,
	}
	for _, a := range c.ArrangedIn {
		v.Arranged = append(v.Arranged, ui.RuleArrangementRow{
			DocRef: a.DocRef,
			Anchor: a.Anchor,
			Depth:  a.Depth,
			Href:   "/" + a.DocRef + "#" + a.Anchor,
		})
	}
	return v
}

// termView maps a resolved term to its page. The rule link uses the rule's
// own project, which is the glossary project for an instance term.
func termView(md *mdrender.Cache, keys mdrender.ProjectKeys, t *model.Term, projectID string) ui.TermView {
	return ui.TermView{
		Page:     ui.PageProps{Title: string("worklode: " + t.Rule.Heading), ActiveGlobal: "knowledge"},
		Term:     *t,
		BodyHTML: md.Body(keys, string(t.Rule.Body)),
		RuleURL:  fmt.Sprintf("/projects/%s/rule/%d", t.Rule.Project, t.Rule.Number),
	}
}
