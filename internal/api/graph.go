// graph.go serves a project's work graph (model.ProjectGraph): the JSON
// API behind lode graph triples.
package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// getProjectGraph handles GET /api/v1/projects/{id}/graph. The project is
// read first so an unknown id 404s rather than answering with an empty graph.
func (s *server) getProjectGraph(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(ctx, projectID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	g, err := s.st.ProjectGraph(ctx, projectID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	g.Docs = s.withProjectKeys(ctx, g.Docs)
	writeJSON(w, http.StatusOK, g)
}

// graphRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var graphRouteDocs = map[string]routeDoc{
	"GET /api/v1/projects/{id}/graph": {
		summary:   "Get a project's work graph",
		responses: map[int]any{http.StatusOK: model.ProjectGraph{}},
	},
}
