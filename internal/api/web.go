// web.go implements the read-only web UI: the application shell (Page, in
// layout.templ) shared by every page, the seven global destinations and the
// project-local destinations spec 032 §2 defines, and /assets/ (self-hosted
// stylesheet and fonts, embedded and served from internal/ui — see
// assetHandler). When OIDC is configured every page route except /assets/ is
// gated by s.webAuth (see oidcweb.go), which requires a valid session
// cookie; /assets/ stays open unconditionally and, when OIDC is
// unconfigured, every route stays open and the bind address is the only
// access control. Pages render server-side HTML with templ components
// (*_templ.go, generated from the .templ files in this package — see
// internal/ui's //go:generate directive, which drives both packages' templ
// and Tailwind builds — which auto-escape all interpolated values) and reuse
// the same assembly functions as the JSON API (assembleBoard,
// assembleTimeline, assembleProjectCockpit) so that logic lives in exactly
// one place.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// FmtTime renders every timestamp the same way across the web UI: UTC,
// "2006-01-02 15:04". Called directly from the .templ files (the plain-func
// successor to the old html/template FuncMap entry of the same job).
func FmtTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") }

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

// basePage carries the fields every page needs from the Page shell
// component (layout.templ): Title, ActiveGlobal. Every page-specific data
// struct embeds it so Page can address those fields the same way regardless
// of which page is being rendered. ActiveGlobal names the one primary-nav
// destination to mark aria-current="page" on ("home", "intake", "projects",
// "work", "reviews", "deliveries", "knowledge"); leave it empty on
// project-scoped pages, whose local project nav carries the current-page
// marker instead — each page must set aria-current="page" exactly once,
// never on both navs.
type basePage struct {
	Title        string
	ActiveGlobal string
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

// boardPageData is rendered by the Board component (board.templ), shared by
// the Home ("/") and Work ("/work") destinations — both show the same
// org-wide board; only the heading and ActiveGlobal differ.
type boardPageData struct {
	basePage
	Board      *boardResponse
	InboxCount int
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

	data := boardPageData{
		basePage:   basePage{Title: title, ActiveGlobal: activeGlobal},
		Board:      board,
		InboxCount: len(issues),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := Board(data).Render(r.Context(), w); err != nil {
		s.log.Error("render board page", "err", err)
	}
}

// projectsPageData is rendered by the Projects component (projects.templ).
type projectsPageData struct {
	basePage
	Projects []store.Project
}

// projectsPage handles GET /projects: the cross-project portfolio (spec 032
// §2), linking each project to its canonical cockpit URL.
func (s *server) projectsPage(w http.ResponseWriter, r *http.Request) {
	projects, err := s.st.ListProjects(r.Context())
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	data := projectsPageData{
		basePage: basePage{Title: "worklode: projects", ActiveGlobal: "projects"},
		Projects: projects,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := Projects(data).Render(r.Context(), w); err != nil {
		s.log.Error("render projects page", "err", err)
	}
}

// placeholderPageData is rendered by the Placeholder component
// (placeholder.templ): an honest "not built yet" page for a global or
// project-scoped destination whose governing spec section is not
// implemented. Cockpit is nil for a global destination (Intake, Reviews,
// Deliveries, Knowledge) and set for a project section (Crew, Deliverables,
// Reviews, Decisions, Documents, Activity), which loads the project first
// and renders the same project-local navigation as the Overview page.
type placeholderPageData struct {
	basePage
	Heading       string
	Message       string
	Cockpit       *cockpitProjection
	ActiveSection string
}

// globalPlaceholder returns a handler for a global destination with no
// implemented capability yet (Intake, Reviews, Deliveries, Knowledge).
func (s *server) globalPlaceholder(destination, heading, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := placeholderPageData{
			basePage: basePage{Title: "worklode: " + heading, ActiveGlobal: destination},
			Heading:  heading,
			Message:  message,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := Placeholder(data).Render(r.Context(), w); err != nil {
			s.log.Error("render placeholder page", "err", err)
		}
	}
}

// projectSections allow-lists the project-local destinations that are not
// implemented yet, one honest-unavailable message per key naming the owning
// spec section. Unknown keys 404 (see projectSectionPage).
var projectSections = map[string]string{
	"crew":         "Crew arrives with project participants in spec 029 §6.1.",
	"deliverables": "Deliverables arrive with spec 029 §7.",
	"reviews":      "Governed approval reviews arrive with spec 029 §7.",
	"decisions":    "Research decisions arrive with specs 028 and 029.",
	"documents":    "Backbone documents arrive with specs 025 and 026.",
	"activity":     "Project activity arrives when the ordered event view is implemented.",
}

// projectSectionTitles gives each projectSections key its display heading.
var projectSectionTitles = map[string]string{
	"crew":         "Crew",
	"deliverables": "Deliverables",
	"reviews":      "Reviews",
	"decisions":    "Decisions",
	"documents":    "Documents",
	"activity":     "Activity",
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

	heading := projectSectionTitles[section]
	data := placeholderPageData{
		basePage:      basePage{Title: "worklode: " + cockpit.Project.Name + ": " + heading},
		Heading:       heading,
		Message:       message,
		Cockpit:       cockpit,
		ActiveSection: section,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := Placeholder(data).Render(r.Context(), w); err != nil {
		s.log.Error("render project section page", "err", err)
	}
}

// webTimelineRow is one rendered row of a task's timeline: a type label and
// a human summary line, derived from the same entries the JSON timeline API
// emits (see assembleTimeline / summarizeEntry). URL is the entry's
// source-native link (set for pr and ci entries only; "" otherwise) —
// rendered as a plain string href in task.templ, so templ's own href
// sanitizer (github.com/a-h/templ's SafeURL) neutralizes an unsafe scheme
// into "about:invalid#TemplFailedSanitizationURL" before it ever reaches
// the page.
type webTimelineRow struct {
	At      time.Time
	Type    string
	Label   string
	Summary string
	URL     string
}

// taskPageData is rendered by the Task component (task.templ).
type taskPageData struct {
	basePage
	Task      store.Task
	Blocked   bool
	Holder    *store.Lease
	Blocks    []string
	BlockedBy []string
	Parent    string
	Children  []string
	Progress  store.HierarchyProgress
	Timeline  []webTimelineRow
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

	data := taskPageData{
		basePage: basePage{Title: "worklode: " + id},
		Task:     *t,
		Blocked:  blocked[id],
	}
	for _, e := range out {
		switch e.Type {
		case "blocks":
			data.Blocks = append(data.Blocks, e.ToTask)
		case "child_of":
			data.Parent = e.ToTask
		}
	}
	for _, e := range in {
		switch e.Type {
		case "blocks":
			data.BlockedBy = append(data.BlockedBy, e.FromTask)
		case "child_of":
			data.Children = append(data.Children, e.FromTask)
		}
	}
	if lease, err := s.st.ActiveLease(ctx, id); err == nil {
		data.Holder = lease
	} else if !errors.Is(err, store.ErrNotFound) {
		s.webStoreErr(w, err)
		return
	}

	// Leaves can never have children, so skip the query — the component
	// only reads Progress inside the len(data.Children) > 0 branch anyway.
	if len(data.Children) > 0 {
		progress, err := s.st.ChildProgress(ctx, id)
		if err != nil {
			s.webStoreErr(w, err)
			return
		}
		data.Progress = progress
	}

	data.Timeline = make([]webTimelineRow, 0, len(entries))
	for _, e := range entries {
		data.Timeline = append(data.Timeline, summarizeEntry(e))
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := Task(data).Render(r.Context(), w); err != nil {
		s.log.Error("render task page", "err", err)
	}
}

// projectPageData is rendered by the Project component (project.templ).
type projectPageData struct {
	basePage
	Cockpit *cockpitProjection
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

	data := projectPageData{
		basePage: basePage{Title: "worklode: " + cockpit.Project.Name},
		Cockpit:  cockpit,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := Project(data).Render(r.Context(), w); err != nil {
		s.log.Error("render project page", "err", err)
	}
}

// summarizeEntry renders one timeline entry (same map[string]any shape the
// JSON timeline API emits, from timeline.go's stateEntries/prEntries/etc.)
// as a human-readable row: a type label plus a one-line summary.
func summarizeEntry(e timelineEntry) webTimelineRow {
	typ, _ := e.obj["type"].(string)
	row := webTimelineRow{At: e.at, Type: typ}
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
