// crew.go serves a project's Crew (spec 029 §6.1, spec 032 §6): the roster
// page (GET /projects/{id}/crew), the JSON API's add and remove routes (POST
// /api/v1/projects/{id}/participants, DELETE
// /api/v1/projects/{id}/participants/{actor}), and the page's own add and
// remove forms (POST /projects/{id}/crew, POST /projects/{id}/crew/remove).
//
// Each membership change has exactly one write function — recordCrewAdd and
// recordCrewRemove — that both surfaces call, so a member added or removed
// in a browser and one changed by the CLI are the same write, recorded under
// the same event type ("crew.member_added" / "crew.member_removed", spec 029
// §8.4), and differ only in the event source that records which surface it
// came from.
package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

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
		RemoveAction: "/projects/" + project.ID + "/crew/remove",
		Roles:        formOptions(store.ParticipantRoles(), defaultCrewRole, ""),
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
func (s *server) recordCrewAdd(ctx context.Context, source, projectID, actorID, role string, lead, deputy bool, by string) (model.CrewMember, error) {
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
		"lead": lead, "deputy": deputy, "by": by,
	}, func(tx *sql.Tx, eventID int64) error {
		return store.AddParticipant(tx, now, projectID, actorID, role, lead, deputy, by, eventID)
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

// listCrewMembers handles GET /api/v1/projects/{id}/participants: the whole
// roster, mapped from store.Participant with toCrewMember. An empty roster
// answers with an empty list (never null — a nil []model.CrewMember marshals
// to null, so the slice is always allocated); an unknown project 404s the
// same way store.ListParticipants's own ErrNotFound does everywhere else.
func (s *server) listCrewMembers(w http.ResponseWriter, r *http.Request) {
	crew, err := s.st.ListParticipants(r.Context(), r.PathValue("id"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	members := make([]model.CrewMember, 0, len(crew))
	for _, p := range crew {
		members = append(members, toCrewMember(p))
	}
	writeJSON(w, http.StatusOK, model.ParticipantListResponse{Participants: members})
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
	member, err := s.recordCrewAdd(r.Context(), "cli", r.PathValue("id"), req.Actor, req.Role, req.Lead, req.Deputy, actorIDFrom(r))
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
	if _, err := s.recordCrewAdd(ctx, "web", project.ID, values.Actor, values.Role, values.Lead, false, actorIDFrom(r)); err != nil {
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
		if values.Role != "" {
			v.Roles = formOptions(store.ParticipantRoles(), values.Role, "")
		}
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
	r, size := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(r)) + msg[size:] + "."
}

// crewMember reads one member off a project's roster. It is how the remove
// path learns the facts only the store holds — the role rows about to be
// deleted — and an actor with no row is ErrNotFound, the same answer
// store.RemoveParticipant gives.
func (s *server) crewMember(ctx context.Context, projectID, actorID string) (model.CrewMember, error) {
	crew, err := s.st.ListParticipants(ctx, projectID)
	if err != nil {
		return model.CrewMember{}, err
	}
	for _, p := range crew {
		if p.ActorID == actorID {
			return toCrewMember(p), nil
		}
	}
	return model.CrewMember{}, fmt.Errorf("actor %s is not on project %s's crew: %w",
		actorID, projectID, store.ErrNotFound)
}

// recordCrewRemove removes one member from a project's Crew — every role row
// they hold, in one act — through RecordEvent under event type
// "crew.member_removed" (spec 029 §8.4). source is "cli" for the JSON API
// and "web" for the cockpit form; by is the acting actor ("" on an open
// instance).
//
// The payload has to name the roles being removed, and s.recordEvent
// marshals the payload before it runs the mutation, so the roster is read
// first and a member that read cannot describe is refused here rather than
// emitting an empty roles array. The mutation stays the authority on whether
// the removal happened: store.RemoveParticipant re-reads the member's rows
// under FOR UPDATE inside the transaction, and its refusal rolls the event
// back with it — so a roster that changed between the two commits nothing.
// The only residue of that race is a payload one role label stale, which
// needs a concurrent add to the very member being removed.
func (s *server) recordCrewRemove(ctx context.Context, source, projectID, actorID, by string) error {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return fmt.Errorf("actor is required: %w", store.ErrInvalidInput)
	}
	member, err := s.crewMember(ctx, projectID, actorID)
	if err != nil {
		return err
	}
	now := s.st.Now()
	return s.recordEvent(ctx, source, "crew.member_removed", map[string]any{
		"project": projectID, "actor": actorID, "roles": member.Roles,
		"lead": member.Lead, "by": by,
	}, func(tx *sql.Tx, eventID int64) error {
		return store.RemoveParticipant(tx, now, projectID, actorID, by, eventID)
	})
}

// removeCrewMember handles DELETE /api/v1/projects/{id}/participants/{actor}:
// drop every role row that actor holds on the project. 204 on success; the
// §6.1 guards come back as 422 with the store's message, which names each
// open item the member still owns, so the caller has the responsibility list
// without a second request.
func (s *server) removeCrewMember(w http.ResponseWriter, r *http.Request) {
	err := s.recordCrewRemove(r.Context(), "cli", r.PathValue("id"), r.PathValue("actor"), actorIDFrom(r))
	if err != nil {
		s.observeCrewChange("api", "remove", crewOutcome(err))
		s.mapStoreErr(w, err)
		return
	}
	s.observeCrewChange("api", "remove", "ok")
	w.WriteHeader(http.StatusNoContent)
}

// removeCrewMemberFromForm handles POST /projects/{id}/crew/remove, the
// per-row Remove button. A refused removal re-renders the roster with the
// reason and, when the reason is open work, that work listed and linked; a
// successful one 303s back to the roster.
func (s *server) removeCrewMemberFromForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, ok := s.beginFormPost(w, r, "crew_remove")
	if !ok {
		return
	}

	actor := strings.TrimSpace(r.PostFormValue("actor"))
	if err := s.recordCrewRemove(ctx, "web", project.ID, actor, actorIDFrom(r)); err != nil {
		s.observeCrewChange("web", "remove", crewOutcome(err))
		s.observeFormSubmission("crew_remove", formOutcome(err))
		// A refused removal is about the project's state — the member owns
		// open work, or is the lead — so it belongs back on the roster, not
		// on an error page. Only a genuine fault falls through.
		if !errors.Is(err, store.ErrInvalidInput) && !errors.Is(err, store.ErrNotFound) {
			s.webStoreErr(w, err)
			return
		}
		s.renderCrewRemovalRefusal(w, r, project, actor, err)
		return
	}
	s.observeCrewChange("web", "remove", "ok")
	// "created" is this metric's word for an accepted submission; the
	// outcome label set is shared with the creation forms, and a
	// removal-only value would leave a dead series on every other form.
	s.observeFormSubmission("crew_remove", "created")
	http.Redirect(w, r, "/projects/"+project.ID+"/crew", http.StatusSeeOther)
}

// renderCrewRemovalRefusal re-renders the roster with a refused removal
// explained. When the member still owns open work, that work is the
// explanation — listed and linked (spec 032 §6's responsibility review),
// read from the same query the store's guard ran, so the page shows exactly
// what is blocking the removal rather than a restatement of the refusal.
func (s *server) renderCrewRemovalRefusal(w http.ResponseWriter, r *http.Request,
	project ui.CockpitProject, actor string, cause error) {
	ctx := r.Context()
	participants, err := s.st.ListParticipants(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	v := crewView(project, participants)

	var open []store.OwnedWork
	if actor != "" {
		if open, err = s.st.OpenWorkOwnedBy(ctx, project.ID, actor); err != nil {
			s.webStoreErr(w, err)
			return
		}
	}
	for _, item := range open {
		v.Responsibilities = append(v.Responsibilities, ui.CrewWorkItem{
			Kind: item.Kind, ID: item.ID, Title: item.Title, State: item.State,
		})
	}
	if len(open) > 0 {
		v.RemoveError = crewMemberLabel(v.Members, actor) +
			" still owns open work on this project. Reassign or close each item below, then remove them."
	} else {
		v.RemoveError = crewRemovalMessage(cause)
	}
	s.renderWeb(w, r, http.StatusUnprocessableEntity, "crew page", ui.Crew(v))
}

// crewMemberLabel names a member the way the roster does — display name when
// there is one, the actor id otherwise — so a message about them reads the
// same as the row it is about.
func crewMemberLabel(members []ui.CrewMember, actorID string) string {
	for _, m := range members {
		if m.ActorID == actorID && m.DisplayName != "" {
			return m.DisplayName
		}
	}
	return actorID
}

// crewRemovalMessage turns a refused removal that is not about open work
// into the sentence the roster shows: an actor who is not on the Crew, or
// the lead, whose refusal already says why in the store's own words.
func crewRemovalMessage(err error) string {
	if errors.Is(err, store.ErrNotFound) {
		return "That actor is not on this project's Crew."
	}
	msg := strings.TrimSuffix(err.Error(), ": "+store.ErrInvalidInput.Error())
	if msg == "" {
		return "That removal was refused."
	}
	r, size := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(r)) + msg[size:] + "."
}
