// crew.go serves a project's Crew (WL-REQ-123, WL-REQ-343) through the JSON
// API: the roster (GET /api/v1/projects/{id}/participants) and its add and
// remove routes (POST /api/v1/projects/{id}/participants, DELETE
// /api/v1/projects/{id}/participants/{actor}). The SPA's Crew page
// (WL-REQ-1802) and the CLI both call them.
//
// Each membership change has exactly one write function, recordCrewAdd and
// recordCrewRemove, recorded under event type "crew.member_added" /
// "crew.member_removed" (WL-REQ-1605).
package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// defaultCrewRole is what an add with no role means. Adding someone to the
// Crew without an opinion about what they do is the common case, and spec
// WL-REQ-123 makes the label descriptive rather than load-bearing, so the
// default is a plain word and not a refusal.
const defaultCrewRole = "member"

// recordCrewAdd adds one role-labelled Crew row through RecordEvent under
// event type "crew.member_added" (WL-REQ-126) and returns the member as
// the roster now shows them — every role they hold, not just the one just
// added. source is the event source; by is the acting actor ("" on an open
// instance, stored as NULL).
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

// listCrew handles GET /api/v1/crew: the instance roster of human actors
// (WL-REQ-1795). Never null when empty.
func (s *server) listCrew(w http.ResponseWriter, r *http.Request) {
	crew, err := s.st.ListHumanActors(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.CrewListResponse{Crew: crew})
}

// toCrewMember is the one conversion point from the store's aggregated
// Participant to the wire shape (WL-RULE-1349).
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
// "crew.member_removed" (WL-REQ-126). source is the event source; by is the
// acting actor ("" on an open instance).
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
// WL-REQ-1605 guards come back as 422 with the store's message, which names each
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

// crewRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var crewRouteDocs = map[string]routeDoc{
	"GET /api/v1/crew": {
		summary:   "List the instance's human actors",
		responses: map[int]any{http.StatusOK: model.CrewListResponse{}},
	},
	"GET /api/v1/projects/{id}/participants": {
		summary:   "List a project's Crew roster",
		responses: map[int]any{http.StatusOK: model.ParticipantListResponse{}},
	},
	"POST /api/v1/projects/{id}/participants": {
		summary:   "Add a member to a project's Crew",
		request:   model.AddCrewMemberInput{},
		responses: map[int]any{http.StatusCreated: model.CrewMember{}},
	},
	"DELETE /api/v1/projects/{id}/participants/{actor}": {
		summary:   "Remove a member from a project's Crew",
		responses: map[int]any{http.StatusNoContent: nil},
	},
}
