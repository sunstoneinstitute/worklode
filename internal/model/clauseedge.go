package model

import "time"

// ClauseEdge is one typed edge between two clauses
// (docs/specs2/12-spec-refactoring-design-tree.md S12, S26). From and To are
// clause refs ("WL-CL-12"). Source is "manual" for an edge an architect
// wrote, "derived" for a references edge the store read out of the clause
// text, and "refactor" for a supersededBy edge lode clause supersede wrote.
type ClauseEdge struct {
	Type        string    `json:"type"` // refines | constrains | conflictsWith | references | supersededBy | wasDerivedFrom
	From        string    `json:"from"`
	FromHeading string    `json:"from_heading"`
	To          string    `json:"to"`
	ToHeading   string    `json:"to_heading"`
	Source      string    `json:"source"` // manual | derived | refactor
	CreatedAt   time.Time `json:"created_at"`
}

// ClauseEdgeInput is the body of POST and DELETE /api/v1/clauses/{id}/edges:
// the edge type and the clause at the other end.
type ClauseEdgeInput struct {
	Type string `json:"type"`
	To   string `json:"to"` // "WL-CL-12"
}
