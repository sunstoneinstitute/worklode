// rulepage.go gives a rule a cockpit page at its canonical URL (S20):
// GET /projects/{proj}/rule/{n} and its version sibling. /rules/{ref} is
// the resolving redirect the autolinker targets, the counterpart of
// /docs/ref/{ref...}. The rest of the URL scheme is in canonicalurl.go.

package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
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
	view.Viewer = actorIDFrom(r)
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

// acceptRuleFromPage handles POST /projects/{id}/rule/{n}/accept: the rule
// page's Accept button, performing `lode rule accept` through the page-script
// write gate (WL-REQ-1338). A body of {"substantive": true} is `--substantive`.
// The store holds the owner check. A rule that is no
// longer draft is a 409, since the button is drawn only on a draft.
func (s *server) acceptRuleFromPage(w http.ResponseWriter, r *http.Request) {
	var body model.AcceptRuleInput
	sub, project, ok := s.beginJSONPost(w, r, "rule/accept", &body)
	if !ok {
		return
	}
	n, err := strconv.ParseInt(r.PathValue("n"), 10, 64)
	if err != nil || n <= 0 {
		s.observeProgressWrite("rule/accept", "refused")
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	c, err := s.st.AcceptRule(r.Context(), project.Key, n, body.Substantive, sub.ActorID)
	switch {
	case errors.Is(err, store.ErrBadTransition):
		s.observeProgressWrite("rule/accept", "conflict")
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		s.observeProgressWrite("rule/accept", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
	default:
		s.observeProgressWrite("rule/accept", "ok")
		writeJSON(w, http.StatusOK, c)
	}
}
