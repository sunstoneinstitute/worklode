// graph.go serves a project's work graph (model.ProjectGraph): the JSON
// API behind lode graph triples.
package api

import "net/http"

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
