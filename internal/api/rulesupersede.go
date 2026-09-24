package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// supersedeRules handles POST /api/v1/projects/{id}/rules/supersede: an
// architect applies a refactor map (12-spec-refactoring-design-tree.md S24),
// withdrawing each old rule and linking it to its successors. The actor
// comes from the request subject, the way ruleedges.go's linkRule does.
func (s *server) supersedeRules(w http.ResponseWriter, r *http.Request) {
	var req model.SupersedeInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(r.Context(), projectID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	res, err := s.st.SupersedeRules(r.Context(), projectID, actorIDFrom(r), req)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
