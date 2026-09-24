// canonicalurl.go serves the S20 canonical URL scheme for tasks and
// documents: /projects/{proj}/{kind}/{n}, with /{ver} for a document
// version. The rule kind has its own literal routes in rulepage.go. The
// root routes /tasks/{id}, /docs/{ref} and /docs/versions/{id}/{n} redirect
// here.

package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// docKinds are the document kinds a canonical URL can name. rule is not
// here: its literal routes win over the {kind} wildcard.
var docKinds = []string{"spec", "adr", "plan"}

// taskCanonicalURL is a task's canonical cockpit URL (S20). It is "" when
// the id is not <projectKey>-<n>, so the caller keeps serving the page at
// /tasks/{id}.
func taskCanonicalURL(t *model.Task, projectKey string) string {
	key, n, ok := model.SplitTaskID(t.ID)
	if !ok || key != projectKey {
		return ""
	}
	return fmt.Sprintf("/projects/%s/%s/%d", t.Project, t.Kind, n)
}

// withQuery appends r's raw query to path, so a redirect keeps parameters
// such as ?body=source.
func withQuery(path string, r *http.Request) string {
	if r.URL.RawQuery == "" {
		return path
	}
	return path + "?" + r.URL.RawQuery
}

// projectEntityPage handles GET /projects/{proj}/{kind}/{n} and its /{ver}
// sibling. A document kind serves the document page, or one version of it; a
// task kind serves the task page, and a task named under another kind
// redirects to its own. Tasks have no versions.
func (s *server) projectEntityPage(w http.ResponseWriter, r *http.Request) {
	p, redirected := s.projectFromPath(w, r, r.PathValue("proj"))
	if p == nil || redirected {
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n <= 0 {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	kind, ver := r.PathValue("kind"), r.PathValue("ver")
	switch {
	case slices.Contains(docKinds, kind):
		s.projectDocPage(w, r, p, kind, n, ver)
	case slices.Contains(ns.TaskKinds, kind) && ver == "":
		t, err := s.st.GetTask(r.Context(), fmt.Sprintf("%s-%d", p.Key, n))
		if err != nil {
			s.webStoreErr(w, err)
			return
		}
		if t.Kind != kind {
			http.Redirect(w, r, withQuery(taskCanonicalURL(t, p.Key), r), http.StatusFound)
			return
		}
		s.renderTaskPage(w, r, t.ID)
	default:
		webErr(w, http.StatusNotFound, "not found")
	}
}

// projectDocPage serves the document branch of projectEntityPage. ?v=<n> is
// a 302 to the version path. The navigation metric is recorded here because
// the route also serves tasks, which are not a navigation destination.
func (s *server) projectDocPage(w http.ResponseWriter, r *http.Request, p *store.Project, kind string, n int, ver string) {
	d, err := s.resolveDocRefWeb(r.Context(), fmt.Sprintf("%s-%s-%d", p.Key, strings.ToUpper(kind), n), "")
	if err != nil || d.Project != p.ID {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	if ver == "" {
		if v := r.URL.Query().Get("v"); v != "" {
			http.Redirect(w, r, docCanonicalURL(d)+"/"+v, http.StatusFound)
			return
		}
	}
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	defer func() { s.observeNavigation("knowledge", navOutcome(sw.status)) }()
	if ver == "" {
		s.renderDocPage(sw, r, d, nil)
		return
	}
	version, err := strconv.Atoi(ver)
	if err != nil || version <= 0 || version > math.MaxInt32 {
		webErr(sw, http.StatusNotFound, "not found")
		return
	}
	s.renderDocVersion(sw, r, d, version)
}

// projectFromPath resolves {proj}: a project id as is; an uppercase project
// key redirects to the same path with the id in its place and reports true.
// A miss writes 404 and returns nil.
//
// The id is tried first and the key only when no project has that id, so a
// project whose id is not lowercase still resolves. createProject checks the
// shape of the key and not of the id, so an uppercase id is legal, and
// reading the case of {proj} to pick the lookup made those projects
// unreachable: no key can match an id.
func (s *server) projectFromPath(w http.ResponseWriter, r *http.Request, proj string) (*store.Project, bool) {
	p, err := s.st.GetProject(r.Context(), proj)
	if err == nil {
		return p, false
	}
	if !errors.Is(err, store.ErrNotFound) {
		s.webStoreErr(w, err)
		return nil, false
	}
	p, err = s.st.ProjectByKey(r.Context(), proj)
	if err != nil {
		webErr(w, http.StatusNotFound, "not found")
		return nil, false
	}
	target := strings.Replace(r.URL.Path, "/projects/"+proj+"/", "/projects/"+p.ID+"/", 1)
	http.Redirect(w, r, withQuery(target, r), http.StatusFound)
	return p, true
}
