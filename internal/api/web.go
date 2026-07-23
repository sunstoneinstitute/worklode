// web.go implements the read-only web UI: GET /, GET /tasks/{id}, and
// GET /projects/{id}. When OIDC is configured these routes are gated by
// s.webAuth (see oidcweb.go), which requires a valid session cookie; when
// OIDC is unconfigured they stay open and the bind address is the only
// access control. They render server-side HTML with html/template (which
// auto-escapes all interpolated values) and reuse the same assembly
// functions as the JSON API (assembleBoard, assembleTimeline) so the board
// and timeline logic lives in exactly one place.
package api

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

// templateFuncs are the funcs available to every web template.
var templateFuncs = template.FuncMap{
	// fmtTime renders every timestamp the same way across the web UI: UTC,
	// "2006-01-02 15:04".
	"fmtTime": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") },
}

// parseWebTemplates parses layout.html plus one page's own template file
// into a *template.Template of their own. Each page gets its own parse (one
// per page, not one combined parse of every file) because html/template
// treats every {{define "content"}} block from every parsed file as
// entries in one shared namespace: parsing board.html, task.html, and
// project.html together would let whichever file parses last silently win
// the "content" block for all three pages. Pairing each page file with
// layout.html on its own keeps every page's "content" in its own set.
func parseWebTemplates(page string) *template.Template {
	return template.Must(template.New("layout.html").Funcs(templateFuncs).
		ParseFS(templatesFS, "templates/layout.html", "templates/"+page))
}

// basePage carries the fields every page template needs from layout.html
// (.Title, .AutoRefresh). Every page-specific data struct embeds it so
// layout.html can address those fields the same way regardless of which
// page is being rendered.
type basePage struct {
	Title       string
	AutoRefresh bool
}

// webErr renders a minimal HTML error page. The web UI has no JSON error
// convention of its own (see writeErr for the API's), so this is its
// equivalent.
func webErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, "<!doctype html><title>%d</title><h1>%d</h1><p>%s</p>", code, code, template.HTMLEscapeString(msg))
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

// boardPageData is rendered by board.html.
type boardPageData struct {
	basePage
	Board      *boardResponse
	InboxCount int
}

// boardPage handles GET / (routed as "GET /{$}" — see server.go — so it
// matches only the exact root path, not every otherwise-unmatched GET): the
// org-wide board, built from the same assembleBoard used by GET
// /api/v1/board, plus a count of new (untriaged) inbox issues.
// Auto-refreshes every 30s.
func (s *server) boardPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	board, err := s.assembleBoard(ctx, "")
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	issues, err := s.st.ListIssues(ctx, "new")
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	data := boardPageData{
		basePage:   basePage{Title: "worklode: board", AutoRefresh: true},
		Board:      board,
		InboxCount: len(issues),
	}
	if err := s.tmplBoard.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.log.Error("render board page", "err", err)
	}
}

// webTimelineRow is one rendered row of a task's timeline: a type label and
// a human summary line, derived from the same entries the JSON timeline API
// emits (see assembleTimeline / summarizeEntry).
type webTimelineRow struct {
	At      time.Time
	Type    string
	Label   string
	Summary string
}

// taskPageData is rendered by task.html.
type taskPageData struct {
	basePage
	Task      store.Task
	Blocked   bool
	Holder    *store.Lease
	Blocks    []string
	BlockedBy []string
	Parent    string
	Children  []string
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

	data.Timeline = make([]webTimelineRow, 0, len(entries))
	for _, e := range entries {
		data.Timeline = append(data.Timeline, summarizeEntry(e))
	}

	if err := s.tmplTask.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.log.Error("render task page", "err", err)
	}
}

// projectPageData is rendered by project.html.
type projectPageData struct {
	basePage
	Project boardProjectJSON
	Repos   []string
}

// projectPage handles GET /projects/{id}: the project's board (scoped via
// assembleBoard) and its mapped repos. Deployments are not project-scoped
// in the schema (they reference an artifact, not a project), so nothing
// deployment-specific is shown here.
func (s *server) projectPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	board, err := s.assembleBoard(ctx, id)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	repos, err := s.st.ListRepos(ctx, id)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	data := projectPageData{
		basePage: basePage{Title: "worklode: " + board.Projects[0].Name, AutoRefresh: true},
		Project:  board.Projects[0],
		Repos:    repos,
	}
	if err := s.tmplProject.ExecuteTemplate(w, "layout.html", data); err != nil {
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
	case "ci":
		row.Label = "CI run"
		summary := fmt.Sprintf("%s: %s", strField(e.obj, "workflow"), strField(e.obj, "status"))
		if c := strPtrField(e.obj, "conclusion"); c != "" {
			summary += " (" + c + ")"
		}
		row.Summary = summary
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
