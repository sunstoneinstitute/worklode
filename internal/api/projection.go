package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// listProjectionFailures handles GET /api/v1/graph/projection/failures: the
// projects the knowledge-graph projector has quarantined, oldest failure
// first (WL-SPEC-79 §12).
//
// Read-only and derived: the rows are the projector's own record of debt it
// still owes, so there is nothing here to correct through this surface. A
// "retry now" verb over the same table is a separate decision, because
// clearing next_attempt_at asks the projector to re-render immediately, which
// is an act rather than a look.
func (s *server) listProjectionFailures(w http.ResponseWriter, r *http.Request) {
	failures, err := s.st.ProjectionFailures(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if failures == nil {
		failures = []model.ProjectionFailure{}
	}
	writeJSON(w, http.StatusOK, model.ProjectionFailureListResponse{Failures: failures})
}

// projectionRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var projectionRouteDocs = map[string]routeDoc{
	"GET /api/v1/graph/projection/failures": {
		summary:   "List projects the knowledge-graph projector has quarantined",
		responses: map[int]any{http.StatusOK: model.ProjectionFailureListResponse{}},
	},
}
