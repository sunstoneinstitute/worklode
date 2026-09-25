package model

import "time"

// RuleEdge is one typed edge between two rules
// (docs/specs2/12-spec-refactoring-design-tree.md S12, S26). From and To are
// rule refs ("WL-RULE-12"). Source is "manual" for an edge an architect
// wrote, "derived" for a references edge the store read out of the rule
// text, and "refactor" for a supersedes edge lode rule supersede wrote.
type RuleEdge struct {
	Type        string    `json:"type"` // refines | constrains | conflictsWith | references | amends | supersedes | wasDerivedFrom
	From        string    `json:"from"`
	FromHeading string    `json:"from_heading"`
	To          string    `json:"to"`
	ToHeading   string    `json:"to_heading"`
	Source      string    `json:"source"` // manual | derived | refactor
	CreatedAt   time.Time `json:"created_at"`
}

// RuleEdgeInput is the body of POST and DELETE /api/v1/rules/{id}/edges:
// the edge type and the rule at the other end.
type RuleEdgeInput struct {
	Type string `json:"type"`
	To   string `json:"to"` // "WL-RULE-12"
}
