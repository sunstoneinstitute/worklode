package model

import "time"

// Rule is a design rule (docs/specs2/12-spec-refactoring-design-tree.md
// S8 to S11, S20): the lowest heading unit of a spec or ADR, with its own
// identity, status and version history. Heading and Body are the current
// version's text.
type Rule struct {
	ID         int64  `json:"id"`
	Project    string `json:"project"`
	ProjectKey string `json:"project_key"`
	// Ref is the citable id, "WL-RULE-12" (025 §14.3 grammar, type RULE).
	Ref     string `json:"ref"`
	Number  int64  `json:"number"`
	Status  string `json:"status"` // draft | accepted | superseded | withdrawn
	Version int    `json:"version"`
	Heading string `json:"heading"`
	Body    string `json:"body"`
	// ArrangedIn lists the documents whose current arrangement holds this
	// rule, and at which version, position and depth.
	ArrangedIn []RuleArrangement `json:"arranged_in"`
	// GovernedTasks are the tasks this rule governs (S2), newest link first.
	GovernedTasks []RuleTask `json:"governed_tasks"`
	// Edges are every typed edge in or out of this rule (S12, S26).
	Edges []RuleEdge `json:"edges"`
	// Owner is an actor id, empty when unowned; Tags are free labels (S15).
	Owner     string    `json:"owner"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RuleArrangement is one document's placement of a rule.
type RuleArrangement struct {
	Doc         int64  `json:"doc"`
	DocRef      string `json:"doc_ref"` // "WL-SPEC-4"
	Anchor      string `json:"anchor"`
	Position    int    `json:"position"`
	Depth       int    `json:"depth"`
	RuleVersion int    `json:"rule_version"`
}

// RuleVersion is one entry of a rule's version history.
type RuleVersion struct {
	Version   int       `json:"version"`
	Heading   string    `json:"heading"`
	CreatedAt time.Time `json:"created_at"`
}

// RuleTask is one task a rule governs, as the rule detail lists it.
type RuleTask struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	State       string `json:"state"`
	Source      string `json:"source"`       // plan | manual
	RuleVersion int    `json:"rule_version"` // version current when the link was made
}

// EditRuleInput is the body of PUT /api/v1/rules/{id}: the rule's new
// heading and body. Both replace what is stored (S35: a draft version is
// rewritten in place, an accepted version becomes the next version when the
// arranging document's revision lands).
type EditRuleInput struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

// RuleMetaInput is the body of PATCH /api/v1/rules/{id} (S15). A nil
// field leaves the column alone; a present field replaces it, so an empty
// Owner clears the owner and an empty Tags clears the tags.
type RuleMetaInput struct {
	Owner *string   `json:"owner,omitempty"`
	Tags  *[]string `json:"tags,omitempty"`
}

// SupersedeEntry is one line of a refactor map (S24, R7): an old rule and
// the rules that continue it. Each ref is "WL-RULE-12" or a section ref,
// "WL-SPEC-4#sec-2". An empty New withdraws the old rule with no successor.
type SupersedeEntry struct {
	Old string   `json:"old"`
	New []string `json:"new"`
}

// SupersedeInput is the body of the refactor request: the map, applied in one
// transaction, or resolved and rolled back when DryRun is set (R6).
type SupersedeInput struct {
	Entries []SupersedeEntry `json:"entries"`
	DryRun  bool             `json:"dry_run,omitempty"`
}

// SupersedeResolved is one map line with every ref resolved to its rule
// ref ("WL-RULE-12").
type SupersedeResolved struct {
	Old string   `json:"old"`
	New []string `json:"new"`
}

// SupersedeResult says what a refactor changed, or would change on a dry
// run: rules withdrawn, supersedes edges written, tasks told through a
// task.governance_superseded event, and plans marked stale. A re-run of a
// map that already applied reports zero for each.
type SupersedeResult struct {
	Entries    []SupersedeResolved `json:"entries"`
	Withdrawn  int                 `json:"withdrawn"`
	Edges      int                 `json:"edges"`
	Tasks      int                 `json:"tasks"`
	StalePlans int                 `json:"stale_plans"`
	DryRun     bool                `json:"dry_run,omitempty"`
}
