// taskblockers.go serves the transitive blocker tree — the thin HTTP skin
// over store.BlockerTree, which is where the walk and its open predicate live.
package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// getTaskBlockers handles GET /api/v1/tasks/{id}/blockers: every open task
// transitively holding the task, plus the unfinished plans ordered before its
// own plan. The brief answers the same question one hop deep; this answers it
// all the way down, so a caller can see which task at the bottom of the chain
// is the one to actually work on.
func (s *server) getTaskBlockers(w http.ResponseWriter, r *http.Request) {
	tree, err := s.st.BlockerTree(r.Context(), r.PathValue("id"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

// getBlockers handles GET /api/v1/blockers?project=<id>: the same walk as
// getTaskBlockers with no task named, rooted at every blocked task in scope.
// An absent project spans every project, the way the board's own scope does.
func (s *server) getBlockers(w http.ResponseWriter, r *http.Request) {
	trees, err := s.st.BlockerForest(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.BlockerForest{Trees: trees})
}
