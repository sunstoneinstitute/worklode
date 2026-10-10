package model

import "time"

// Rule is a design rule (WL-SPEC-77): the lowest heading unit of a spec or ADR, with its own
// identity, status and version history. Heading and Body are the current
// version's text.
type Rule struct {
	ID         int64  `json:"id"`
	Project    string `json:"project"`
	ProjectKey string `json:"project_key"`
	// Ref is the citable id with its kind's infix: "WL-REQ-12" for a
	// requirement, "WL-RULE-12" otherwise (WL-SPEC-77 §4).
	Ref    string `json:"ref"`
	Number int64  `json:"number"`
	// Kind is requirement, catalogue, invariant, definition or principle
	// (WL-SPEC-77 §4). informative is retired and reads as a principle.
	Kind    string `json:"kind"`
	Status  string `json:"status"` // draft | accepted | superseded | withdrawn
	Version int    `json:"version"`
	Heading string `json:"heading"`
	Body    string `json:"body"`
	// ConceptIRI is the ns/concept.ttl concept a definition defines, empty
	// when it names none (WL-SPEC-77 §4d).
	ConceptIRI string `json:"concept_iri"`
	// ArrangedIn lists the documents whose current arrangement holds this
	// rule, and at which version, position and depth.
	ArrangedIn []RuleArrangement `json:"arranged_in"`
	// CoveredBy lists the plans covering this rule, directly or through a
	// rule it supersedes (WL-SPEC-77 §4). GetRule fills it; a list leaves it
	// empty.
	CoveredBy []RulePlan `json:"covered_by"`
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
	DocRef      string `json:"doc_ref"` // "WL-SPEC-75"
	Anchor      string `json:"anchor"`
	Position    int    `json:"position"`
	Depth       int    `json:"depth"`
	RuleVersion int    `json:"rule_version"`
}

// RulePlan is one plan covering a rule.
type RulePlan struct {
	Doc    int64  `json:"doc"`
	DocRef string `json:"doc_ref"` // "WL-PLAN-7"
	Status string `json:"status"`
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
// heading and body. Both replace what is stored: a draft version is
// rewritten in place, an accepted one gets the next version as a draft
// (WL-SPEC-77 §19.4).
type EditRuleInput struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

// AcceptRuleInput is the optional body of POST /api/v1/rules/{id}/accept.
// Substantive is the author's judgment that the version is substantive
// (WL-SPEC-77 §10), which applies the gates when no mechanical check does.
type AcceptRuleInput struct {
	Substantive bool `json:"substantive,omitempty"`
}

// RuleMetaInput is the body of PATCH /api/v1/rules/{id} (S15). A nil
// field leaves the column alone; a present field replaces it, so an empty
// Owner clears the owner and an empty Tags clears the tags.
type RuleMetaInput struct {
	Owner *string   `json:"owner,omitempty"`
	Tags  *[]string `json:"tags,omitempty"`
	Kind  *string   `json:"kind,omitempty"` // requirement | catalogue | invariant | definition | principle
	// Concept is a definition's concept IRI from ns/concept.ttl; "" clears it.
	Concept *string `json:"concept,omitempty"`
}

// SupersedeEntry is one line of a refactor map (S24, R7): an old rule and
// the rules that continue it. Each ref is "WL-RULE-12" or a section ref,
// "WL-SPEC-75#sec-2". An empty New withdraws the old rule with no successor.
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

// RuleListParams is the query string of GET /api/v1/rules. Doc is any
// document ref (WL-SPEC-73, a slug, an id); when set, the response is that
// document's arrangement in order instead of every rule by number.
type RuleListParams struct {
	Project string `query:"project,omitempty"`
	Status  string `query:"status,omitempty"`
	Doc     string `query:"doc,omitempty"`
}

// RuleClosure is GET /api/v1/rules/{id}/closure: a rule and every rule
// reachable from it over refines and needs, in breadth-first order
// (WL-SPEC-77 §4c). Members[0] is the rule itself. Words is the sum of the
// members' body word counts.
type RuleClosure struct {
	Rule    string              `json:"rule"`
	Members []RuleClosureMember `json:"members"`
	Words   int                 `json:"words"`
}

// RuleClosureMember is one rule in a closure. Edge and From name the edge
// it was first reached by ("needs" from "WL-REQ-12"); both are empty on the
// rule the closure was read for.
type RuleClosureMember struct {
	Ref     string `json:"ref"`
	Kind    string `json:"kind"`
	Heading string `json:"heading"`
	Words   int    `json:"words"`
	Edge    string `json:"edge,omitempty"` // refines | needs
	From    string `json:"from,omitempty"`
}

// RuleLint is GET /api/v1/projects/{id}/rules/lint: a project's corpus
// measured against the targets of WL-SPEC-77 §4c. Rules counts the project's
// draft and accepted rules; the closure spreads are over those rules.
type RuleLint struct {
	Project      string            `json:"project"`
	Rules        int               `json:"rules"`
	Specs        []RuleLintSpec    `json:"specs"`
	ClosureRules RuleLintSpread    `json:"closure_rules"`
	ClosureWords RuleLintSpread    `json:"closure_words"`
	Findings     []RuleLintFinding `json:"findings"`
}

// RuleLintSpec is one live spec and the number of rules it arranges.
type RuleLintSpec struct {
	Spec  string `json:"spec"`
	Rules int    `json:"rules"`
}

// RuleLintSpread is the median and 90th percentile (nearest rank) of a
// closure measure.
type RuleLintSpread struct {
	Median int `json:"median"`
	P90    int `json:"p90"`
}

// RuleLintFinding is one line of the report, naming its rule. Check is
// one-context-edge or converted-heading (reports, not verdicts),
// undefined-term, conflict, positional-reference or unresolved-ref (a rule
// ref in the rule's text that names no rule). A converted-heading finding
// names a rule the WL-SPEC-77 §19.7 migration deleted.
type RuleLintFinding struct {
	Rule   string `json:"rule"`
	Check  string `json:"check"`
	Detail string `json:"detail"`
}

// AddRuleInput is the body of POST /api/v1/rules: a standalone rule,
// arranged in no document, created at draft version 1 and owned by the
// caller (WL-SPEC-77 §19.2). Kind is one of the five rule kinds and
// defaults to requirement.
type AddRuleInput struct {
	Project string   `json:"project"`
	Heading string   `json:"heading"`
	Body    string   `json:"body"`
	Tags    []string `json:"tags,omitempty"`
	Kind    string   `json:"kind,omitempty"`
}

// ArrangeRuleInput is the body of POST /api/v1/docs/{id}/rules: place an
// existing rule in a spec (WL-SPEC-77 §19.3). After and Under each name a
// rule ref or a section anchor of the spec, at most one of them; with
// neither the rule goes last. Anchor defaults to the next free number at
// that position.
type ArrangeRuleInput struct {
	Rule   string `json:"rule"`
	After  string `json:"after,omitempty"`
	Under  string `json:"under,omitempty"`
	Anchor string `json:"anchor,omitempty"`
}
