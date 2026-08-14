// web.go implements the web UI's read surfaces (its writes — the two
// creation forms — live in webform.go): it builds the presentation views
// internal/ui's templ components render (the shared Page shell, the seven
// global destinations and the project-local destinations spec 032 §2 defines)
// and serves /assets/ (self-hosted stylesheet and fonts, embedded and served
// from internal/ui — see assetHandler). When OIDC is configured every page
// route except /assets/ is gated by s.webAuth (see oidcweb.go), which
// requires a valid session cookie; /assets/ stays open unconditionally and,
// when OIDC is unconfigured, every route stays open and the bind address is
// the only access control. Each handler maps the read model into a ui view
// (see render.go) and calls ui.<Page>(view).Render; the views reuse the same
// assembly functions as the JSON API (assembleBoard, assembleTimeline,
// assembleProjectCockpit) so that logic lives in exactly one place.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// assetHandler serves internal/ui's embedded /assets/ tree (stylesheet and
// self-hosted fonts) outside webAuth: they carry no project data, so an
// OIDC-gated deployment must not redirect them to login (a stylesheet
// request has no session to attach a redirect to). Cache-Control is bounded
// (an hour) rather than immutable/forever, since asset filenames are not
// content-hashed. Every response is counted under the "asset" navigation
// destination (see navWrap).
func (s *server) assetHandler() http.Handler {
	fileServer := http.StripPrefix("/assets/", http.FileServer(http.FS(ui.Assets())))
	return s.navWrap("asset", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fileServer.ServeHTTP(w, r)
	})
}

// navOutcome classifies a response status for the
// worklode_web_navigation_requests_total outcome label.
func navOutcome(status int) string {
	switch {
	case status == http.StatusNotFound:
		return "not_found"
	case status >= 500:
		return "error"
	case status >= 400:
		// Reachable only on the creation-form routes (a refused cross-origin
		// POST, a rejected submit); a read page is 200, 404, or 500.
		return "rejected"
	default:
		return "ok"
	}
}

// navWrap wraps a web handler to record one worklode_web_navigation_requests_total
// observation for the given destination, classified from the handler's final
// HTTP status code.
func (s *server) navWrap(destination string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next(sw, r)
		s.observeNavigation(destination, navOutcome(sw.status))
	}
}

// webErr renders a minimal HTML error page. The web UI has no JSON error
// convention of its own (see writeErr for the API's), so this is its
// equivalent.
func webErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, "<!doctype html><title>%d</title><h1>%d</h1><p>%s</p>", code, code, html.EscapeString(msg))
}

// webStoreErr maps a store error to a web error page: ErrNotFound -> 404,
// anything else -> 500 (logged, detail not leaked — same policy as
// mapStoreErr for the JSON API).
func (s *server) webStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	s.log.Error("internal error", "err", err)
	webErr(w, http.StatusInternalServerError, "internal error")
}

// homePage handles GET / (routed as "GET /{$}" — see server.go — so it
// matches only the exact root path, not every otherwise-unmatched GET): the
// default post-login destination. Part 1 has no assigned-work/decision/brief
// aggregation yet (spec 032 §9), so Home shows the org-wide board under a
// "Current work" heading — the same data "/work" shows as the task-oriented
// destination.
func (s *server) homePage(w http.ResponseWriter, r *http.Request) {
	s.renderBoard(w, r, "home", "worklode: home")
}

// workPage handles GET /work: task-oriented saved queries and the ready
// frontier (spec 032 §2). Part 1 renders the same org-wide board as Home,
// without the "Current work" framing.
func (s *server) workPage(w http.ResponseWriter, r *http.Request) {
	s.renderBoard(w, r, "work", "worklode: work")
}

// renderBoard builds the org-wide board, built from the same assembleBoard
// used by GET /api/v1/board, plus a count of new (untriaged) inbox issues,
// and renders it under the given ActiveGlobal/title. Shared by homePage and
// workPage.
func (s *server) renderBoard(w http.ResponseWriter, r *http.Request, activeGlobal, title string) {
	ctx := r.Context()

	board, err := s.assembleBoard(ctx, "")
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	issues, err := s.st.ListIssues(ctx, "new", "")
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	view := boardView(board, len(issues), activeGlobal == "home", title, activeGlobal)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.Board(view).Render(r.Context(), w); err != nil {
		s.log.Error("render board page", "err", err)
	}
}

// projectsPage handles GET /projects: the cross-project portfolio (spec 032
// §2), linking each project to its canonical cockpit URL.
func (s *server) projectsPage(w http.ResponseWriter, r *http.Request) {
	projects, err := s.st.ListProjects(r.Context())
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	view := projectsView(projects, "worklode: projects", "projects")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.Projects(view).Render(r.Context(), w); err != nil {
		s.log.Error("render projects page", "err", err)
	}
}

// globalPlaceholder returns a handler for a global destination with no
// implemented capability yet (Intake, Reviews, Deliveries, Knowledge). The
// rendered page is honest: heading and owning-spec message only, no form,
// button, count, or fabricated record.
func (s *server) globalPlaceholder(destination, heading, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		view := placeholderGlobalView(destination, heading, message)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := ui.Placeholder(view).Render(r.Context(), w); err != nil {
			s.log.Error("render placeholder page", "err", err)
		}
	}
}

// projectSections allow-lists the project-local destinations that are not
// implemented yet, one honest-unavailable message per key naming the owning
// spec section. Unknown keys 404 (see projectSectionPage).
// Deliverables is absent: it is a built destination now (see webform.go's
// deliverablesPage), routed ahead of this wildcard.
var projectSections = map[string]string{
	"crew":      "Crew arrives with project participants in spec 029 §6.1.",
	"reviews":   "Governed approval reviews arrive with spec 029 §7.",
	"decisions": "Research decisions arrive with specs 025 and 029.",
	"documents": "Backbone documents arrive with specs 025 and 026.",
	"activity":  "Project activity arrives when the ordered event view is implemented.",
}

// projectSectionTitles gives each projectSections key its display heading.
var projectSectionTitles = map[string]string{
	"crew":      "Crew",
	"reviews":   "Reviews",
	"decisions": "Decisions",
	"documents": "Documents",
	"activity":  "Activity",
}

// projectSectionPage handles GET /projects/{id}/{section}: an honest
// placeholder for a not-yet-implemented project-local destination. It loads
// the project cockpit first (so an unknown project 404s the same way
// projectPage does) and renders the same project header/navigation as the
// Overview page, naming the missing capability. It has no form, button,
// count, or fake record — see the global constraints in the plan.
func (s *server) projectSectionPage(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("section")
	message, ok := projectSections[section]
	if !ok {
		webErr(w, http.StatusNotFound, "not found")
		return
	}

	cockpit, err := s.assembleProjectCockpit(r.Context(), r.PathValue("id"))
	s.observeCockpitProjection("web", err)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	view := placeholderProjectView(cockpit, projectSectionTitles[section], message, section)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.Placeholder(view).Render(r.Context(), w); err != nil {
		s.log.Error("render project section page", "err", err)
	}
}

// taskPage handles GET /tasks/{id}: title, state, priority/kind, project,
// body, lease holder (if any), edges, and the full timeline — built from the
// same assembleTimeline used by GET /api/v1/tasks/{id}/timeline.
func (s *server) taskPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	t, entries, err := s.assembleTimeline(ctx, id)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	blocked, err := s.st.BlockedTaskIDs(ctx)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	out, in, err := s.st.ListEdges(ctx, id)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	view := ui.TaskView{
		Page:     ui.PageProps{Title: "worklode: " + id},
		Task:     *t,
		Blocked:  blocked[id],
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
	if lease, err := s.st.ActiveLease(ctx, id); err == nil {
		view.Holder = lease
	} else if !errors.Is(err, store.ErrNotFound) {
		s.webStoreErr(w, err)
		return
	}

	// Leaves can never have children, so skip the query — the component
	// only reads Progress inside the len(Children) > 0 branch anyway.
	if len(view.Children) > 0 {
		progress, err := s.st.ChildProgress(ctx, id)
		if err != nil {
			s.webStoreErr(w, err)
			return
		}
		view.Progress = progress
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.Task(view).Render(r.Context(), w); err != nil {
		s.log.Error("render task page", "err", err)
	}
}

// projectPage handles GET /projects/{id}: the project cockpit, via the same
// assembleProjectCockpit used by GET /api/v1/projects/{id}/cockpit — it must
// not call its own HTTP API. Deployments are not project-scoped in the
// schema (they reference an artifact, not a project), so nothing
// deployment-specific is shown here.
func (s *server) projectPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	cockpit, err := s.assembleProjectCockpit(ctx, id)
	s.observeCockpitProjection("web", err)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	view := cockpitView(cockpit, "worklode: "+cockpit.Project.Name)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.Cockpit(view).Render(r.Context(), w); err != nil {
		s.log.Error("render project page", "err", err)
	}
}

// summarizeEntry renders one timeline entry (same map[string]any shape the
// JSON timeline API emits, from timeline.go's stateEntries/prEntries/etc.)
// as a human-readable row: a type label plus a one-line summary.
func summarizeEntry(e timelineEntry) ui.TimelineRow {
	typ, _ := e.obj["type"].(string)
	row := ui.TimelineRow{At: e.at, Type: typ}
	switch typ {
	case "state":
		row.Label = "State change"
		row.Summary = summarizeStateChange(e.obj)
	case "pr":
		row.Label = "Pull request"
		row.Summary = fmt.Sprintf("%s#%d %q %s", strField(e.obj, "repo"), i64Field(e.obj, "number"), strField(e.obj, "title"), strField(e.obj, "state"))
		row.URL = strField(e.obj, "url")
	case "ci":
		row.Label = "CI run"
		summary := fmt.Sprintf("%s: %s", strField(e.obj, "workflow"), strField(e.obj, "status"))
		if c := strPtrField(e.obj, "conclusion"); c != "" {
			summary += " (" + c + ")"
		}
		row.Summary = summary
		row.URL = strField(e.obj, "url")
	case "review":
		row.Label = "Review"
		row.Summary = fmt.Sprintf("%s: %s", strField(e.obj, "reviewer"), strField(e.obj, "state"))
	case "artifact":
		row.Label = "Artifact"
		row.Summary = fmt.Sprintf("%s %s %s built", strField(e.obj, "kind"), strField(e.obj, "name"), strField(e.obj, "version"))
	case "deployment":
		row.Label = "Deployment"
		row.Summary = fmt.Sprintf("%s (%s): %s", strField(e.obj, "environment"), strField(e.obj, "target_name"), strField(e.obj, "status"))
	case "runtime":
		row.Label = "Runtime event"
		row.Summary = fmt.Sprintf("%s on %s/%s: %s", strField(e.obj, "kind"), strField(e.obj, "cluster"), strField(e.obj, "workload"), strField(e.obj, "message"))
	case "landed":
		row.Label = "Landed"
		row.Summary = fmt.Sprintf("%s %s on main", strField(e.obj, "repo"), shortSHA(strField(e.obj, "sha")))
	case "deployed":
		row.Label = "Delivered"
		row.Summary = fmt.Sprintf("%s confirmed in %s", strField(e.obj, "repo"), strField(e.obj, "environment"))
	case "released":
		row.Label = "Released"
		row.Summary = fmt.Sprintf("%s %s", strField(e.obj, "repo"), strField(e.obj, "tag"))
	default:
		row.Label = typ
	}
	return row
}

// summarizeStateChange decodes a state-log "change" payload (written by
// store.LogChange: {"field": ..., "old": ..., "new": ...}, "old" omitted for
// a plain field update) into a one-line summary.
func summarizeStateChange(obj map[string]any) string {
	raw, ok := obj["change"].(json.RawMessage)
	if !ok {
		return ""
	}
	var change struct {
		Field string `json:"field"`
		Old   string `json:"old"`
		New   string `json:"new"`
	}
	if err := json.Unmarshal(raw, &change); err != nil {
		return ""
	}
	if change.Old == "" {
		return fmt.Sprintf("%s set to %s", change.Field, change.New)
	}
	return fmt.Sprintf("%s: %s -> %s", change.Field, change.Old, change.New)
}

// shortSHA abbreviates a commit sha for display.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func strField(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func strPtrField(m map[string]any, k string) string {
	if v, ok := m[k].(*string); ok && v != nil {
		return *v
	}
	return ""
}

func i64Field(m map[string]any, k string) int64 {
	v, _ := m[k].(int64)
	return v
}
