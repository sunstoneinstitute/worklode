// rulepage.go gives a rule a cockpit page at its canonical URL (S20):
// GET /projects/{proj}/rule/{n} and its version sibling. /rules/{ref} is
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

// rulePage handles GET /projects/{proj}/rule/{n} and its /{ver} sibling:
// the canonical rule URL (S20).
func (s *server) rulePage(w http.ResponseWriter, r *http.Request) {
	p, redirected := s.projectFromPath(w, r, r.PathValue("proj"))
	if p == nil || redirected {
		return
	}
	n, err := strconv.ParseInt(r.PathValue("n"), 10, 64)
	if err != nil || n <= 0 {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	var c *model.Rule
	if ver := r.PathValue("ver"); ver != "" {
		var v int
		v, err = strconv.Atoi(ver)
		if err != nil || v <= 0 {
			webErr(w, http.StatusNotFound, "not found")
			return
		}
		c, err = s.st.GetRuleVersion(r.Context(), p.Key, n, v)
	} else {
		c, err = s.st.GetRule(r.Context(), p.Key, n)
	}
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	versions, err := s.st.ListRuleVersions(r.Context(), p.Key, n)
	if err != nil {
		s.log.Warn("rendering rule page without version history", "rule", c.Ref, "err", err)
	}
	view := ruleView(s.mdcache, s.projectKeys(r.Context(), p.ID), c, versions, p.ID)
	s.renderWeb(w, r, http.StatusOK, "rule page", ui.Rule(view))
}

// ruleRefRedirect handles GET /rules/{ref}: the resolving redirect the
// autolinker targets, the counterpart of /docs/ref/.
func (s *server) ruleRefRedirect(w http.ResponseWriter, r *http.Request) {
	ref, ok := designdoc.ParseRuleRef(r.PathValue("ref"))
	if !ok {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	p, err := s.st.ProjectByKey(r.Context(), ref.Key)
	if err != nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%s/rule/%d", p.ID, ref.Number), http.StatusFound)
}
