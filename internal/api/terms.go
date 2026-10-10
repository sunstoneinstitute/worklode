// terms.go serves definition rules as terms (WL-SPEC-77 §4d): the term page
// at /projects/{proj}/term/{slug} and the project's term list. A term's
// rule keeps its own page at /projects/{proj}/rule/{n}.

package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// listTerms handles GET /api/v1/projects/{id}/terms: the project's own
// definitions with their slugs. No dedicated metric: a list is
// http_requests_total's {route, code}.
func (s *server) listTerms(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(r.Context(), projectID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	terms, err := s.st.ListTerms(r.Context(), projectID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, terms)
}

// termPage handles GET /projects/{proj}/term/{slug}: the term resolved in
// the project, its own definition first, then the instance glossary's.
func (s *server) termPage(w http.ResponseWriter, r *http.Request) {
	p, redirected := s.projectFromPath(w, r, r.PathValue("proj"))
	if p == nil || redirected {
		return
	}
	term, err := s.st.ResolveTerm(r.Context(), p.ID, r.PathValue("slug"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	view := termView(s.mdcache, s.projectKeys(r.Context(), p.ID), term, p.ID)
	s.renderWeb(w, r, http.StatusOK, "term page", ui.Term(view))
}

// termRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var termRouteDocs = map[string]routeDoc{
	"GET /api/v1/projects/{id}/terms": {
		summary:   "List a project's definition rules as terms, with their slugs",
		responses: map[int]any{http.StatusOK: []model.Term{}},
	},
}
