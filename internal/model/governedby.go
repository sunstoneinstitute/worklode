package model

// TaskGovernance is one governing rule of a task
// (docs/specs2/12-spec-refactoring-design-tree.md S2, S10): the rule, the
// version the link was made against, and the rule's current version so a
// reader can see when the governing text has moved on.
type TaskGovernance struct {
	Rule        string `json:"rule"`         // "WL-RULE-12"
	RuleVersion int    `json:"rule_version"` // version current when the link was made
	Current     int    `json:"current"`      // the rule's current version
	Heading     string `json:"heading"`
	Status      string `json:"status"`
	Source      string `json:"source"`           // plan | manual
	Pinned      int    `json:"pinned,omitempty"` // the version the link is pinned to, 0 when it follows the newest
	URL         string `json:"url"`              // canonical rule page, with /<ver> when pinned (S10, S20)

	// ResolvesTo is the live rules reached from a withdrawn governing
	// rule by following supersedes edges back from it, transitively (S22, R8): empty
	// when the governing rule is live.
	ResolvesTo []string `json:"resolves_to,omitempty"`
}

// GovernInput names a rule to add to, or remove from, a task's governing
// set: the body of POST and DELETE /api/v1/tasks/{id}/governed-by. Pin is
// read only by POST; DELETE ignores it.
type GovernInput struct {
	Rule string `json:"rule"`          // "WL-RULE-12"
	Pin  bool   `json:"pin,omitempty"` // pin the link to the version current when it is made (S10)
}
