// clausepage.go gives a clause a cockpit page at its canonical URL (S20):
// GET /projects/{proj}/clause/{n}, its version sibling, and the two
// redirects that fill out the URL scheme — a project key typed by habit, and
// the document-kind form (spec/adr/plan only; task kinds and /<ver> on
// documents are deferred past this increment, R5). /clauses/{ref} is the
// resolving redirect the autolinker targets, the counterpart of
// /docs/ref/{ref...}.

package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// clausePage handles GET /projects/{proj}/clause/{n} and its /{ver} sibling:
// the canonical clause URL (S20).
func (s *server) clausePage(w http.ResponseWriter, r *http.Request) {
	p, redirected := s.projectFromPath(w, r, r.PathValue("proj"))
	if p == nil || redirected {
		return
	}
	n, err := strconv.ParseInt(r.PathValue("n"), 10, 64)
	if err != nil || n <= 0 {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	var c *model.Clause
	if ver := r.PathValue("ver"); ver != "" {
		var v int
		v, err = strconv.Atoi(ver)
		if err != nil || v <= 0 {
			webErr(w, http.StatusNotFound, "not found")
			return
		}
		c, err = s.st.GetClauseVersion(r.Context(), p.Key, n, v)
	} else {
		c, err = s.st.GetClause(r.Context(), p.Key, n)
	}
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	versions, err := s.st.ListClauseVersions(r.Context(), p.Key, n)
	if err != nil {
		s.log.Warn("rendering clause page without version history", "clause", c.Ref, "err", err)
	}
	view := clauseView(s.mdcache, s.projectKeys(r.Context(), p.ID), c, versions, p.ID)
	s.renderWeb(w, r, http.StatusOK, "clause page", ui.Clause(view))
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
	http.Redirect(w, r, strings.Replace(r.URL.Path, "/projects/"+proj+"/", "/projects/"+p.ID+"/", 1), http.StatusFound)
	return p, true
}

// projectKindRedirect handles GET /projects/{proj}/{kind}/{n} for the
// document kinds: the /docs/<KEY>-<KIND>-<n> page stays canonical for
// documents in this increment, so the S20 form redirects there. Task kinds
// are deferred (R5, recorded in a spec by task 6) and 404.
func (s *server) projectKindRedirect(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if kind != "spec" && kind != "adr" && kind != "plan" {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	p, redirected := s.projectFromPath(w, r, r.PathValue("proj"))
	if p == nil || redirected {
		return
	}
	n := r.PathValue("n")
	if _, err := strconv.Atoi(n); err != nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/docs/%s-%s-%s", p.Key, strings.ToUpper(kind), n), http.StatusFound)
}

// clauseRefRedirect handles GET /clauses/{ref}: the resolving redirect the
// autolinker targets, the counterpart of /docs/ref/.
func (s *server) clauseRefRedirect(w http.ResponseWriter, r *http.Request) {
	ref, ok := designdoc.ParseClauseRef(r.PathValue("ref"))
	if !ok {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	p, err := s.st.ProjectByKey(r.Context(), ref.Key)
	if err != nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%s/clause/%d", p.ID, ref.Number), http.StatusFound)
}
