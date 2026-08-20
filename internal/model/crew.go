package model

import "time"

// CrewMember is the wire form of one Crew member of a project (spec 029
// §6.1): an actor holding at least one role-labelled participation row,
// folded to one entry per actor. Roles is the sorted set of labels that
// actor holds on the project; at most one member of a project has Lead set
// (spec 032 §6's accountable human). AddedAt is the earliest of the actor's
// role rows.
type CrewMember struct {
	Actor       string    `json:"actor"`
	DisplayName string    `json:"display_name"`
	Roles       []string  `json:"roles"`
	Lead        bool      `json:"lead"`
	AddedAt     time.Time `json:"added_at"`
}

// ParticipantListResponse is the response body of GET
// /api/v1/projects/{id}/participants. An empty roster is an empty list, not
// null.
type ParticipantListResponse struct {
	Participants []CrewMember `json:"participants"`
}

// AddCrewMemberInput is the request body of POST
// /api/v1/projects/{id}/participants. Actor is required; an empty Role
// defaults to "member" server-side, so adding someone to the Crew without an
// opinion about what they do is one field. Role is a free-form label, not an
// enum — what a person does on a project is org vocabulary.
type AddCrewMemberInput struct {
	Actor string `json:"actor"`
	Role  string `json:"role"`
	Lead  bool   `json:"lead"`
}
