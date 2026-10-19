// ideafindings.go serves the idea findings routes (WL-REQ-1800). The store
// decides visibility: a private idea is 409 to its author, 404 to anyone else.
package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// listLitmusTests handles GET /api/v1/ideas/{id}/litmus-tests.
func (s *server) listLitmusTests(w http.ResponseWriter, r *http.Request) {
	l, err := s.st.ListLitmusTests(r.Context(), actorIDFrom(r), r.PathValue("id"))
	if err != nil {
		s.writeIdeaErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.LitmusTestListResponse{LitmusTests: l})
}

// addLitmusTest handles POST /api/v1/ideas/{id}/litmus-tests.
func (s *server) addLitmusTest(w http.ResponseWriter, r *http.Request) {
	var in model.CreateLitmusTestInput
	if err := readJSON(w, r, &in); err != nil {
		writeBodyErr(w, err)
		return
	}
	lt, err := s.st.AddLitmusTest(r.Context(), actorIDFrom(r), r.PathValue("id"), in)
	if err != nil {
		s.writeIdeaErr(w, err)
		return
	}
	s.observeIdeaWrite("litmus")
	writeJSON(w, http.StatusCreated, lt)
}

// getPreResearch handles GET /api/v1/ideas/{id}/pre-research.
func (s *server) getPreResearch(w http.ResponseWriter, r *http.Request) {
	pr, err := s.st.LatestPreResearch(r.Context(), actorIDFrom(r), r.PathValue("id"))
	if err != nil {
		s.writeIdeaErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pr)
}

// putPreResearch handles PUT /api/v1/ideas/{id}/pre-research.
func (s *server) putPreResearch(w http.ResponseWriter, r *http.Request) {
	var in model.PutPreResearchInput
	if err := readJSON(w, r, &in); err != nil {
		writeBodyErr(w, err)
		return
	}
	pr, err := s.st.PutPreResearch(r.Context(), actorIDFrom(r), r.PathValue("id"), in)
	if err != nil {
		s.writeIdeaErr(w, err)
		return
	}
	s.observeIdeaWrite("pre_research")
	writeJSON(w, http.StatusOK, pr)
}

// ideaFindingRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var ideaFindingRouteDocs = map[string]routeDoc{
	"GET /api/v1/ideas/{id}/litmus-tests": {
		summary:   "List a shared idea's litmus tests, newest date first",
		responses: map[int]any{http.StatusOK: model.LitmusTestListResponse{}},
	},
	"POST /api/v1/ideas/{id}/litmus-tests": {
		summary:   "Record a litmus test on a shared idea",
		request:   model.CreateLitmusTestInput{},
		responses: map[int]any{http.StatusCreated: model.LitmusTest{}},
	},
	"GET /api/v1/ideas/{id}/pre-research": {
		summary:   "Read a shared idea's newest pre-research findings",
		responses: map[int]any{http.StatusOK: model.PreResearch{}},
	},
	"PUT /api/v1/ideas/{id}/pre-research": {
		summary:   "Record a pre-research run on a shared idea",
		request:   model.PutPreResearchInput{},
		responses: map[int]any{http.StatusOK: model.PreResearch{}},
	},
}
