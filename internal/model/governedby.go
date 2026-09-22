package model

// TaskGovernance is one governing clause of a task
// (docs/specs2/12-spec-refactoring-design-tree.md S2, S10): the clause, the
// version the link was made against, and the clause's current version so a
// reader can see when the governing text has moved on.
type TaskGovernance struct {
	Clause        string `json:"clause"`         // "WL-CL-12"
	ClauseVersion int    `json:"clause_version"` // version current when the link was made
	Current       int    `json:"current"`        // the clause's current version
	Heading       string `json:"heading"`
	Status        string `json:"status"`
	Source        string `json:"source"` // plan | manual
}

// GovernInput names a clause to add to, or remove from, a task's governing
// set: the body of POST and DELETE /api/v1/tasks/{id}/governed-by.
type GovernInput struct {
	Clause string `json:"clause"` // "WL-CL-12"
}
