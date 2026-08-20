// crew.go implements the Crew project-local destination (GET
// /projects/{id}/crew): the read-only roster of a project's Crew (spec 029
// §6.1, spec 032 §6). Mutations (adding/removing a Crew member) arrive in
// later tasks; this is a projection over internal/store's ListParticipants,
// like every other project read page.
package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// crewPage handles GET /projects/{id}/crew. It loads the project header for
// shell identity (so an unknown project 404s the same way every other
// project route does), then the roster; an empty roster renders the honest
// "No Crew yet" state, never a fabricated row.
func (s *server) crewPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.projectHeader(ctx, r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	participants, err := s.st.ListParticipants(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	s.renderWeb(w, r, http.StatusOK, "crew page", ui.Crew(crewView(project, participants)))
}

// crewView maps a project's Crew roster (internal/store's Participant, one
// per actor holding at least one role) into the Crew page's view type.
func crewView(project ui.CockpitProject, participants []store.Participant) ui.CrewView {
	v := ui.CrewView{
		Page:         ui.PageProps{Title: "worklode: " + project.Name + ": Crew"},
		CanonicalURL: "/projects/" + project.ID + "/crew",
		Project:      project,
		Members:      make([]ui.CrewMember, 0, len(participants)),
	}
	for _, p := range participants {
		v.Members = append(v.Members, ui.CrewMember{
			ActorID:     p.ActorID,
			DisplayName: p.DisplayName,
			Roles:       p.Roles,
			IsLead:      p.IsLead,
		})
	}
	return v
}
