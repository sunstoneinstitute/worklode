package model

import "time"

// Clause is a design clause (docs/specs2/12-spec-refactoring-design-tree.md
// S8 to S11, S20): the lowest heading unit of a spec or ADR, with its own
// identity, status and version history. Heading and Body are the current
// version's text.
type Clause struct {
	ID         int64  `json:"id"`
	Project    string `json:"project"`
	ProjectKey string `json:"project_key"`
	// Ref is the citable id, "WL-CL-12" (025 §14.3 grammar, type CL).
	Ref     string `json:"ref"`
	Number  int64  `json:"number"`
	Status  string `json:"status"` // draft | accepted | superseded | withdrawn
	Version int    `json:"version"`
	Heading string `json:"heading"`
	Body    string `json:"body"`
	// ArrangedIn lists the documents whose current arrangement holds this
	// clause, and at which version, position and depth.
	ArrangedIn []ClauseArrangement `json:"arranged_in"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
}

// ClauseArrangement is one document's placement of a clause.
type ClauseArrangement struct {
	Doc           int64  `json:"doc"`
	DocRef        string `json:"doc_ref"` // "WL-SPEC-4"
	Anchor        string `json:"anchor"`
	Position      int    `json:"position"`
	Depth         int    `json:"depth"`
	ClauseVersion int    `json:"clause_version"`
}
