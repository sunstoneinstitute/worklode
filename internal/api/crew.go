// crew.go serves a project's Crew (spec 029 §6.1, spec 032 §6): the roster
// page (GET /projects/{id}/crew), the JSON API's add-member route (POST
// /api/v1/projects/{id}/participants), and the page's own add-member form
// (POST /projects/{id}/crew).
//
// Both write surfaces go through one function, recordCrewAdd, so a member
// added in a browser and one added by the CLI are the same write, recorded
// under the same event type ("crew.member_added", spec 029 §8.4), and differ
// only in the event source that records which surface it came from.
package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
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
		AddAction:    "/projects/" + project.ID + "/crew",
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

// defaultCrewRole is what an add with no role means. Adding someone to the
// Crew without an opinion about what they do is the common case, and spec
// 029 §6.1 makes the label descriptive rather than load-bearing, so the
// default is a plain word and not a refusal.
const defaultCrewRole = "member"

// recordCrewAdd adds one role-labelled Crew row through RecordEvent under
// event type "crew.member_added" (spec 029 §8.4) and returns the member as
// the roster now shows them — every role they hold, not just the one just
// added. source is "cli" for the JSON API and "web" for the cockpit form,
// which is the only difference between the two paths. by is the acting actor
// ("" on an open instance, stored as NULL).
func (s *server) recordCrewAdd(ctx context.Context, source, projectID, actorID, role string, lead bool, by string) (model.CrewMember, error) {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return model.CrewMember{}, fmt.Errorf("actor is required: %w", store.ErrInvalidInput)
	}
	role = strings.TrimSpace(role)
	if role == "" {
		role = defaultCrewRole
	}
	now := s.st.Now()

	if err := s.recordEvent(ctx, source, "crew.member_added", map[string]any{
		"project": projectID, "actor": actorID, "roles": []string{role},
		"lead": lead, "by": by,
	}, func(tx *sql.Tx, eventID int64) error {
		return store.AddParticipant(tx, now, projectID, actorID, role, lead, by, eventID)
	}); err != nil {
		return model.CrewMember{}, err
	}

	// Read the member back off the roster rather than assembling the
	// response from the request: the actor's display name and their other
	// role labels are facts the store holds, and echoing the input would
	// answer with less than what was written.
	crew, err := s.st.ListParticipants(ctx, projectID)
	if err != nil {
		return model.CrewMember{}, err
	}
	for _, p := range crew {
		if p.ActorID == actorID {
			return toCrewMember(p), nil
		}
	}
	return model.CrewMember{}, fmt.Errorf("crew member %s vanished from project %s after the add", actorID, projectID)
}

// toCrewMember is the one conversion point from the store's aggregated
// Participant to the wire shape (ADR 036).
func toCrewMember(p store.Participant) model.CrewMember {
	return model.CrewMember{
		Actor:       p.ActorID,
		DisplayName: p.DisplayName,
		Roles:       p.Roles,
		Lead:        p.IsLead,
		AddedAt:     p.AddedAt,
	}
}

// addCrewMember handles POST /api/v1/projects/{id}/participants: add one
// role-labelled Crew row and answer 201 with the member as the roster now
// shows them.
func (s *server) addCrewMember(w http.ResponseWriter, r *http.Request) {
	var req model.AddCrewMemberInput
	if err := readJSON(w, r, &req); err != nil {
		s.observeCrewChange("api", "add", "rejected")
		writeBodyErr(w, err)
		return
	}
	member, err := s.recordCrewAdd(r.Context(), "cli", r.PathValue("id"), req.Actor, req.Role, req.Lead, actorIDFrom(r))
	if err != nil {
		s.observeCrewChange("api", "add", crewOutcome(err))
		s.mapStoreErr(w, err)
		return
	}
	s.observeCrewChange("api", "add", "ok")
	writeJSON(w, http.StatusCreated, member)
}

// addCrewMemberFromForm handles POST /projects/{id}/crew, the roster page's
// own add affordance. A rejected add re-renders the roster with the message
// and the typed values; a successful one 303s back to the roster, so a
// reload never adds a second row.
func (s *server) addCrewMemberFromForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, ok := s.beginFormPost(w, r, "crew_add")
	if !ok {
		return
	}

	values := ui.CrewFormValues{
		Actor: strings.TrimSpace(r.PostFormValue("actor")),
		Role:  strings.TrimSpace(r.PostFormValue("role")),
		Lead:  r.PostFormValue("lead") != "",
	}
	if _, err := s.recordCrewAdd(ctx, "web", project.ID, values.Actor, values.Role, values.Lead, actorIDFrom(r)); err != nil {
		s.observeCrewChange("web", "add", crewOutcome(err))
		s.observeFormSubmission("crew_add", formOutcome(err))
		// A refused add is about what was typed — an unknown actor id, a
		// role already held, a second lead — so it belongs back on the form,
		// not on an error page. Only a genuine fault falls through.
		if !errors.Is(err, store.ErrInvalidInput) && !errors.Is(err, store.ErrNotFound) {
			s.webStoreErr(w, err)
			return
		}
		participants, listErr := s.st.ListParticipants(ctx, project.ID)
		if listErr != nil {
			s.webStoreErr(w, listErr)
			return
		}
		v := crewView(project, participants)
		v.Add = values
		v.AddError = crewFormMessage(err)
		s.renderWeb(w, r, http.StatusUnprocessableEntity, "crew page", ui.Crew(v))
		return
	}
	s.observeCrewChange("web", "add", "ok")
	s.observeFormSubmission("crew_add", "created")
	http.Redirect(w, r, "/projects/"+project.ID+"/crew", http.StatusSeeOther)
}

// crewFormMessage turns a refused add into the sentence the form shows. An
// unknown actor gets its own wording because the id was typed and the fix is
// to check it; every other refusal already names the conflict in the store's
// own message, which is what the person has to act on.
func crewFormMessage(err error) string {
	if errors.Is(err, store.ErrNotFound) {
		return "No actor with that id. Check the id, or create the actor first."
	}
	msg := strings.TrimSuffix(err.Error(), ": "+store.ErrInvalidInput.Error())
	if msg == "" {
		return "That add was refused."
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "."
}
