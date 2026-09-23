// clausepage.go gives a clause a cockpit page at its canonical URL (S20):
// GET /projects/{proj}/clause/{n} and its version sibling. /clauses/{ref} is
// the resolving redirect the autolinker targets, the counterpart of
// /docs/ref/{ref...}. The rest of the URL scheme is in canonicalurl.go.

package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
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
