// rally.go serves a project's active rally — the thin HTTP skin over
// store.ActiveRally and store.BlockerTree, which is where the reads live.
package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// getProjectRally handles GET /api/v1/projects/{id}/rally: the project's
// active rally task plus the transitive tree of open tasks it is waiting on.
// 404 when the project has no active rally — a draft rally is not one, and
// store.ActiveRally reports either case as ErrNotFound, the same sentinel a
// missing project would produce, so this does not distinguish them.
func (s *server) getProjectRally(w http.ResponseWriter, r *http.Request) {
	rally, err := s.st.ActiveRally(r.Context(), r.PathValue("id"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	tree, err := s.st.BlockerTree(r.Context(), rally.ID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.Rally{Task: *rally, Blockers: tree})
}
