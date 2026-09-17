// graph.go serves a project's work graph (model.ProjectGraph): the JSON
// API for lode graph triples, the cockpit's Graph page, and the same body
// behind that page's session for the script that draws it.
package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/ui"
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

// graphPage handles GET /projects/{id}/graph: the shell, with the data URL
// the page script fetches. The project is read once for the header and the
// graph once to decide the empty state; the script fetches it again, which
// keeps the page's markup free of embedded data (CSP: no inline script).
func (s *server) graphPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.projectHeader(ctx, r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	g, err := s.st.ProjectGraph(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	s.renderWeb(w, r, http.StatusOK, "graph page", ui.Graph(ui.GraphView{
		Page:         ui.PageProps{Title: "worklode: " + project.Name + ": Graph"},
		CanonicalURL: "/projects/" + project.ID + "/graph",
		Project:      project,
		DataURL:      "/projects/" + project.ID + "/graph/data",
		Empty:        len(g.Tasks) == 0 && len(g.Docs) == 0,
	}))
}

// graphData handles GET /projects/{id}/graph/data: the model.ProjectGraph
// the page script draws, behind the session rather than a bearer token.
func (s *server) graphData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(ctx, projectID); err != nil {
		s.webStoreErr(w, err)
		return
	}
	g, err := s.st.ProjectGraph(ctx, projectID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	g.Docs = s.withProjectKeys(ctx, g.Docs)
	writeJSON(w, http.StatusOK, g)
}
