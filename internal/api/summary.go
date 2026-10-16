// summary.go serves GET /api/v1/projects/{id}/summary (WL-REQ-1797): the
// project summary derived from store facts by internal/summary.
package api

import (
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/summary"
)

// summaryTaskKind is the kind segment of a summary task link. The facts carry
// no task kind, and projectEntityPage redirects a task named under another
// kind to its own, so any valid kind resolves.
const summaryTaskKind = "feature"

// getProjectSummary handles GET /api/v1/projects/{id}/summary. "Today" is the
// server's local date.
func (s *server) getProjectSummary(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	facts, err := s.st.ProjectSummaryFacts(r.Context(), p.ID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	taskURL := func(id string) string {
		return taskCanonicalURL(&model.Task{ID: id, Project: p.ID, Kind: summaryTaskKind}, p.Key)
	}
	writeJSON(w, http.StatusOK, summary.Derive(facts, time.Now(), taskURL))
}

// summaryRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var summaryRouteDocs = map[string]routeDoc{
	"GET /api/v1/projects/{id}/summary": {
		summary:   "Summarise a project's past-due, awaiting-review and per-member work",
		responses: map[int]any{http.StatusOK: model.ProjectSummary{}},
	},
}
