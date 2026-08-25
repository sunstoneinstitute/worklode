// web.go implements the web UI's read surfaces (its writes — the two
// creation forms — live in webform.go): it builds the presentation views
// internal/ui's templ components render (the shared Page shell, the eight
// global destinations and the project-local destinations spec 032 §2 defines)
// and serves /assets/ (self-hosted stylesheet and fonts, embedded and served
// from internal/ui — see assetHandler). When OIDC is configured every page
// route except /assets/ is gated by webGuard (see authz.go), which
// requires a valid session cookie; /assets/ stays open unconditionally and,
// when OIDC is unconfigured, every route stays open and the bind address is
// the only access control. Each handler maps the read model into a ui view
// (see render.go) and calls ui.<Page>(view).Render; the views reuse the same
// assembly functions as the JSON API (assembleBoard, assembleTimeline,
// assembleProjectCockpit) so that logic lives in exactly one place.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/mdrender"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/overview"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// assetHandler serves internal/ui's embedded /assets/ tree (stylesheet and
// self-hosted fonts) outside webGuard: they carry no project data, so an
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
// HTTP status code. It also collects the destination, so initNavMetrics can
// pre-initialise exactly the series the registered routes can emit — the list
// used to be restated by hand in metrics.go, where a new page silently
// shipped without its zero-series.
//
// Registration-time only: the append to s.navDestinations is unsynchronised
// and not idempotent, which is safe because registerRoutes is the sole caller
// and runs once per NewServer, before any request is served. Calling this from
// a request path would both race and grow the slice without bound.
func (s *server) navWrap(destination string, next http.HandlerFunc) http.HandlerFunc {
	s.navDestinations = append(s.navDestinations, destination)
	return func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next(sw, r)
		s.observeNavigation(destination, navOutcome(sw.status))
	}
}

// blobOrigin is the object-storage origin a /blob/{hash} redirect lands on,
// for the page CSP: a redirect target must match the source list by origin
// (paths are ignored on a redirect), so img-src/media-src have to name it.
// Empty when blob storage is unconfigured — a server with no blob storage
// serves no blob redirect, so there is nothing to allow.
func (s *server) blobOrigin() string {
	if s.cfg.BlobEndpoint == "" {
		return ""
	}
	u, err := url.Parse(s.cfg.BlobEndpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// contentSecurityPolicy is the policy every rendered page carries (set in one
// place, renderWeb). Each directive is what the pages actually load:
//
//   - script-src 'self': layout.templ's /assets/theme.js, /assets/nav.js, and
//     /assets/htmx.min.js, and cliauth.templ's /assets/copy.js. No page has
//     an inline script.
//   - style-src 'self': /assets/app.css, and nothing else. No page carries a
//     style attribute or a <style> element, and layout.templ's htmx-config
//     meta turns off the unnonced <style> htmx would otherwise inject for its
//     indicator class — which is what let 'unsafe-inline' go (WL-227). Adding
//     either back breaks the page rather than silently loosening the policy.
//   - font-src 'self': app.css's @font-face files under /assets/fonts/.
//   - img-src/media-src: a rendered task body embeds /blob/{hash}, which
//     redirects to presigned object storage — see blobOrigin.
//   - object-src/base-uri/frame-ancestors 'none': nothing here embeds a
//     plugin, sets a <base>, or is meant to be framed.
//   - form-action 'self': the two creation forms post to this origin only.
//
// default-src 'self' covers the rest (connect, worker, manifest, frame).
func (s *server) contentSecurityPolicy() string {
	media := selfAnd(s.blobOrigin())
	return strings.Join([]string{
		"default-src 'self'",
		"img-src " + media,
		"media-src " + media,
		"script-src 'self'",
		"style-src 'self'",
		"font-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
	}, "; ")
}

// selfAnd builds a source list of 'self' plus one optional origin, without
// the stray space an unconfigured origin would otherwise leave behind.
func selfAnd(origin string) string {
	if origin == "" {
		return "'self'"
	}
	return "'self' " + origin
}

// webErr renders a minimal HTML error page. The web UI has no JSON error
// convention of its own (see writeErr for the API's), so this is its
// equivalent.
func webErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// An error page loads nothing at all — no stylesheet, script, or image —
	// so it gets a tighter policy than renderWeb's rather than that one
	// loosened to a free function with no server to read the blob origin from.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
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
// default post-login destination, which is the actor's project list (spec 032
// §9). It renders in one of three modes, which are also the bounded label
// values of worklode_web_home_renders_total:
//
//   - "open": no actor (LODE_WEB_OPEN, or no login provider). Every project
//     by last activity, and never a role badge or signal line — an anonymous
//     viewer has no relationship to claim. Membership and awaiting approvals
//     are not even read.
//   - "actor": a signed-in actor with at least one card (a project they are
//     on, or one with approvals awaiting them).
//   - "empty": a signed-in actor on no project with nothing awaiting them.
//     The page says so and points at /projects; it never fabricates a card.
//
// Groups come off the request subject, which carries the actor row's stored
// Keycloak claim (see authz.go's Subject.Groups) — the same rows GetActor
// would return, without a second read.
func (s *server) homePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sub := subjectFrom(r)

	projects, err := s.st.ListProjects(ctx)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	workFacts, err := s.st.ListProjectWorkFacts(ctx, "")
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	participants, err := s.st.ListParticipants(ctx, "")
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	in := homeInputs{
		Projects:     projects,
		Facts:        map[string][]store.ProjectWorkFact{},
		Participants: map[string][]string{},
		OpenMode:     sub.ActorID == "",
	}
	for _, f := range workFacts {
		in.Facts[f.Task.Project] = append(in.Facts[f.Task.Project], f)
	}
	// ListParticipants already orders lead first within each project, so
	// appending in row order gives the lead-first crew the cards want. A
	// nameless actor falls back to its id, so no card renders a blank avatar.
	for _, p := range participants {
		name := p.DisplayName
		if name == "" {
			name = p.ActorID
		}
		in.Participants[p.ProjectID] = append(in.Participants[p.ProjectID], name)
	}

	if !in.OpenMode {
		mine, err := s.st.ProjectsForActor(ctx, sub.ActorID)
		if err != nil {
			s.webStoreErr(w, err)
			return
		}
		in.Membership = make(map[string]memberFacts, len(mine))
		for _, ap := range mine {
			in.Membership[ap.Project.ID] = memberFacts{IsLead: ap.IsLead}
		}

		awaiting, err := s.st.ApprovalsAwaiting(ctx, sub.ActorID, sub.Groups)
		if err != nil {
			s.webStoreErr(w, err)
			return
		}
		in.Awaiting = make(map[string]int, len(awaiting))
		for _, c := range awaiting {
			in.Awaiting[c.ProjectID] = c.Count
		}
	}

	cards := homeCards(in)
	mode := homeModeActor
	switch {
	case in.OpenMode:
		mode = homeModeOpen
	case len(cards) == 0:
		mode = homeModeEmpty
	}
	s.observeHomeRender(mode)

	s.renderWeb(w, r, http.StatusOK, "home page", ui.Home(ui.HomeView{
		Page:  ui.PageProps{Title: "worklode: home", ActiveGlobal: "home"},
		Mode:  mode,
		Cards: cards,
	}))
}

// workPage handles GET /work: task-oriented saved queries and the ready
// frontier (spec 032 §2). Part 1 renders the org-wide board, built from the
// same assembleBoard used by GET /api/v1/board, plus a count of new
// (untriaged) inbox issues.
func (s *server) workPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	board, err := s.assembleBoard(ctx, "")
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	newIssues, err := s.st.CountIssues(ctx, "new")
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	view := boardView(board, newIssues, "worklode: work", "work")
	s.renderWeb(w, r, http.StatusOK, "board page", ui.Board(view))
}

// driftPage handles GET /drift: spec 007's read surface as a page — the ready
// frontier and the critical path from the backbone, violations, stale intent
// and gaps from the knowledge graph. It is read-only: the page offers no act,
// because resolving drift means changing a declaration or the code.
//
// An unconfigured graph is not an error here. The JSON API answers 503 for a
// graph-backed read (see failedOverviewRead) because a client asked for
// exactly that read; the page asked for all four, two of which are
// backbone-authoritative, so ErrNoGraph degrades the page to its honest half
// the way overview.Roll degrades its counts.
func (s *server) driftPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	frontier, err := s.overview.Frontier(ctx, "")
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	cp, err := s.overview.CriticalPath(ctx)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	graphEnabled := true
	drift, err := s.overview.DriftReport(ctx, false)
	if errors.Is(err, overview.ErrNoGraph) {
		graphEnabled, drift, err = false, nil, nil
	}
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	var gaps []model.Gap
	if graphEnabled {
		gaps, err = s.overview.GapReport(ctx)
		if errors.Is(err, overview.ErrNoGraph) {
			graphEnabled, gaps, err = false, nil, nil
		}
		if err != nil {
			s.webStoreErr(w, err)
			return
		}
	}

	view := driftView(frontier, cp, drift, gaps, graphEnabled)
	s.renderWeb(w, r, http.StatusOK, "drift page", ui.Drift(view))
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
	s.renderWeb(w, r, http.StatusOK, "projects page", ui.Projects(view))
}

// reviewsPage handles GET /reviews: the awaiting-approvals queue (spec 029
// §7.1), joining migration, store, and shell over real data. Every row
// renders a decide form posting to the act route (029 §7.3).
func (s *server) reviewsPage(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.ListAwaitingApprovals(r.Context())
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	s.renderWeb(w, r, http.StatusOK, "reviews page", ui.Approvals(approvalsView(rows, s.st.Now())))
}

// globalPlaceholder returns a handler for a global destination with no
// implemented capability yet (Intake, Deliveries). The rendered page is
// honest: heading and owning-spec message only, no form, button, count, or
// fabricated record.
func (s *server) globalPlaceholder(destination, heading, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.renderWeb(w, r, http.StatusOK, "placeholder page",
			ui.Placeholder(placeholderGlobalView(destination, heading, message)))
	}
}

// projectSections allow-lists the project-local destinations that are not
// implemented yet, one honest-unavailable message per key naming the owning
// spec section. Unknown keys 404 (see projectSectionPage).
// Deliverables is absent: it is a built destination now (see webform.go's
// deliverablesPage), routed ahead of this wildcard. Crew is absent for the
// same reason: it is a built destination now (see crew.go's crewPage).
var projectSections = map[string]struct{ Title, Message string }{
	"reviews":   {"Reviews", "Governed approval reviews arrive with spec 029 §7."},
	"decisions": {"Decisions", "Research decisions arrive with specs 025 and 029."},
	"documents": {"Documents", "Backbone documents arrive with specs 025 and 026."},
	"activity":  {"Activity", "Project activity arrives when the ordered event view is implemented."},
}

// projectSectionPage handles GET /projects/{id}/{section}: an honest
// placeholder for a not-yet-implemented project-local destination. It loads
// the project cockpit first (so an unknown project 404s the same way
// projectPage does) and renders the same project header/navigation as the
// Overview page, naming the missing capability. It has no form, button,
// count, or fake record — see the global constraints in the plan.
func (s *server) projectSectionPage(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("section")
	sec, ok := projectSections[section]
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

	view := placeholderProjectView(cockpit, sec.Title, sec.Message, section)
	s.renderWeb(w, r, http.StatusOK, "project section page", ui.Placeholder(view))
}

// projectKeys reads the live project-key set that mdrender's autolinker needs
// to tell a bare task id (WL-129) from an acronym that happens to share its
// shape (UTF-8, SHA-256) — see internal/mdrender/autolink.go for why the
// renderer cannot decide that on its own.
//
// Read per render rather than cached on the server. It is one indexed SELECT
// over a table with a row per project, on pages that already run several
// queries, and reading it live is what makes a newly created project's tasks
// link on the next page view instead of after a restart. mdrender's cache key
// carries the set's fingerprint, so the render cache stays correct across a
// change without any invalidation here.
//
// A failed read degrades to the empty set — no task links — rather than
// failing the page: the body still renders, and no link is better than a
// wrong one. Logged rather than counted, because the request it degrades is
// already counted by the HTTP metrics and this adds no outcome of its own.
func (s *server) projectKeys(ctx context.Context) mdrender.ProjectKeys {
	projects, err := s.st.ListProjects(ctx)
	if err != nil {
		s.log.Warn("rendering without task-id links: project keys unreadable", "err", err)
		return mdrender.ProjectKeys{}
	}
	keys := make([]string, 0, len(projects))
	for _, p := range projects {
		keys = append(keys, p.Key)
	}
	return mdrender.NewProjectKeys(keys)
}

// taskPage handles GET /tasks/{id}: title, state, priority/kind, project,
// body (rendered as sanitised markdown — see taskView), attachments, lease
// holder (if any), edges, and the full timeline — built from the same
// assembleTimeline used by GET /api/v1/tasks/{id}/timeline.
func (s *server) taskPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	t, entries, err := s.assembleTimeline(ctx, id)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	blocked, err := s.st.IsTaskBlocked(ctx, id)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	out, in, err := s.st.ListEdges(ctx, id)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	// The reference graph row, embedded and attached alike — the same list
	// GET /api/v1/tasks/{id}/blobs answers with, URL filled in here for the
	// same reason (see listTaskBlobs): the store leaves it empty.
	refs, err := s.st.ListTaskBlobs(ctx, id)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	for i := range refs {
		refs[i].URL = blobURL(refs[i].Hash, refs[i].Filename)
	}

	view := taskView(s.mdcache, s.projectKeys(ctx), t, blocked, entries, out, in)
	view.Attachments = refs
	if lease, err := s.st.ActiveLease(ctx, id); err == nil {
		l := toLeaseJSON(lease)
		view.Holder = &l
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

	s.renderWeb(w, r, http.StatusOK, "task page", ui.Task(view))
}

// docsPage handles GET /docs: the whole document corpus (spec 025 §5), in
// ListDocs's corpus order — project, kind, number (plans last), slug.
// Read-only, like the document page below.
func (s *server) docsPage(w http.ResponseWriter, r *http.Request) {
	docs, err := s.st.ListDocs(r.Context(), docFilterFrom(r))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	view := docsView(docs, s.projectKeyByID(r.Context()))
	s.renderWeb(w, r, http.StatusOK, "docs page", ui.Docs(view))
}

// docPage handles GET /docs/{ref}: numbered documents use their shorthand;
// plans retain their database id because the corpus defines no plan shorthand.
func (s *server) docPage(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.PathValue("id"))
	if ref == "" {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	var d model.Doc
	var detail *model.DocDetail
	var err error
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		detail, err = s.docDetail(r, id)
		if err != nil || detail.Number != 0 && detail.Tombstone == nil {
			webErr(w, http.StatusNotFound, "not found")
			return
		}
		d = detail.Doc
	} else {
		resolved, err := s.resolveDocRefWeb(r.Context(), ref)
		if err != nil {
			webErr(w, http.StatusNotFound, "not found")
			return
		}
		p, err := s.st.GetProject(r.Context(), resolved.Project)
		if err != nil || resolved.Number != 0 && ref != docWebRef(resolved, p.Key) {
			webErr(w, http.StatusNotFound, "not found")
			return
		}
		d = resolved
	}
	if detail == nil {
		detail, err = s.docDetail(r, d.ID)
		if err != nil {
			s.webStoreErr(w, err)
			return
		}
	}
	view := docView(s.mdcache, s.projectKeys(r.Context()), detail)
	s.renderWeb(w, r, http.StatusOK, "doc page", ui.Doc(view))
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
	s.renderWeb(w, r, http.StatusOK, "project page", ui.Cockpit(view))
}

// summarizeEntry renders one timeline entry as a human-readable row: a type
// label plus a one-line summary. Which fields of the entry are populated
// follows from its Type — see model.TimelineEntry.
func summarizeEntry(e model.TimelineEntry) ui.TimelineRow {
	row := ui.TimelineRow{At: e.At, Type: e.Type}
	switch e.Type {
	case "state":
		row.Label = "State change"
		row.Summary = summarizeStateChange(e.Change)
	case "pr":
		row.Label = "Pull request"
		row.Summary = fmt.Sprintf("%s#%d %q %s", e.Repo, e.Number, e.Title, e.State)
		row.URL = e.URL
	case "ci":
		row.Label = "CI run"
		summary := fmt.Sprintf("%s: %s", e.Workflow, e.Status)
		if e.Conclusion != nil && *e.Conclusion != "" {
			summary += " (" + *e.Conclusion + ")"
		}
		row.Summary = summary
		row.URL = e.URL
	case "review":
		row.Label = "Review"
		row.Summary = fmt.Sprintf("%s: %s", e.Reviewer, e.State)
	case "artifact":
		row.Label = "Artifact"
		row.Summary = fmt.Sprintf("%s %s %s built", e.Kind, e.Name, e.Version)
	case "deployment":
		row.Label = "Deployment"
		row.Summary = fmt.Sprintf("%s (%s): %s", e.Environment, e.TargetName, e.Status)
	case "runtime":
		row.Label = "Runtime event"
		row.Summary = fmt.Sprintf("%s on %s/%s: %s", e.Kind, e.Cluster, e.Workload, e.Message)
	case "landed":
		row.Label = "Landed"
		row.Summary = fmt.Sprintf("%s %s on main", e.Repo, shortSHA(e.SHA))
	case "deployed":
		row.Label = "Delivered"
		row.Summary = fmt.Sprintf("%s confirmed in %s", e.Repo, e.Environment)
	case "released":
		row.Label = "Released"
		row.Summary = fmt.Sprintf("%s %s", e.Repo, e.Tag)
	default:
		row.Label = e.Type
	}
	return row
}

// stateChange is the state_log "change" payload store.LogChange writes: a
// stored row, not an HTTP body, which is why it is declared here rather than
// in internal/model (ADR 036 §3). Field "edge" (store.AddEdge / RemoveEdge)
// uses Op/Type/From/To instead of Old/New — see summarizeStateChange.
type stateChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
	Op    string `json:"op"`
	Type  string `json:"type"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// summarizeStateChange decodes a state-log "change" payload into a one-line
// summary. Two shapes: a plain field update (store.LogChange callers like
// Transition/UpdateTaskFields: {"field", "old", "new"}, "old" omitted for a
// create), and an edge change (store.AddEdge/RemoveEdge:
// {"field":"edge","op","type","from","to"}).
func summarizeStateChange(raw json.RawMessage) string {
	var change stateChange
	if err := json.Unmarshal(raw, &change); err != nil {
		return ""
	}
	if change.Field == "edge" {
		verb := "added"
		if change.Op == "remove" {
			verb = "removed"
		}
		return fmt.Sprintf("edge %s: %s %s %s", verb, change.From, change.Type, change.To)
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
