package model

// The spec 007 read surface: drift, gaps, the frontier mirror and the
// estimate-free critical path. internal/overview computes these,
// internal/api serializes them and internal/cli decodes them, so ADR 036
// puts the one declaration here.

// DriftEdge is one dct:requires edge present in exactly one layer.
type DriftEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Deviation is one wl:AcceptedDeviation (spec 006 §Accepted deviations).
type Deviation struct {
	From         string `json:"from"`
	To           string `json:"to"`
	SanctionedBy string `json:"sanctioned_by"`
	ValidUntil   string `json:"valid_until,omitempty"`
	Expired      bool   `json:"expired"`
}

// Gap is a 4.2 finding: a component with no governing doc, or an unmatched
// repo path.
type Gap struct {
	Component string `json:"component,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Path      string `json:"path,omitempty"`
}

// GapList is the response envelope of the gap report.
type GapList struct {
	Gaps []Gap `json:"gaps"`
}

// Claim is one wl:implements edge (025 §11.5): a component's claim that its
// code satisfies a section.
type Claim struct {
	Component string `json:"component"`
	Section   string `json:"section"`
}

// DocCoverage is one document's implementation coverage: how many of its
// non-superseded sections some component claims.
type DocCoverage struct {
	Doc         string `json:"doc"`
	Implemented int    `json:"implemented"`
	Total       int    `json:"total"`
}

// FrontierTask is one row of the frontier mirror, annotated with the
// overview-only critical-path measures (never consumed by claim --next).
type FrontierTask struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Project    string `json:"project"`
	Priority   string `json:"priority"`
	Concern    string `json:"concern,omitempty"`
	FanOut     int    `json:"fan_out"`
	Depth      int    `json:"depth"`
	IsCritical bool   `json:"is_critical"`
}

// FrontierList is the response envelope of the frontier mirror.
type FrontierList struct {
	Tasks []FrontierTask `json:"tasks"`
}

// CriticalPath is the `lode task critical-path` payload.
type CriticalPath struct {
	MaxDepth int `json:"max_depth"`
	// Tasks are the critical tasks by depth. The rows come from the DAG, not
	// from a task read, so Title, Project and Priority are always zero here —
	// only ID, Depth, FanOut and IsCritical are populated. Callers wanting the
	// other three read /api/v1/frontier or the task itself.
	Tasks  []FrontierTask `json:"tasks"`
	Cycles [][]string     `json:"cycles,omitempty"`
}

// Drift bundles the three 4.1 reads.
type Drift struct {
	Violations   []DriftEdge `json:"violations"`
	StaleIntent  []DriftEdge `json:"stale_intent"`
	Acknowledged []Deviation `json:"acknowledged,omitempty"`
}

// Overview is the one-screen roll-up. The 025 §11.5 coverage reads land here
// as totals: the per-document rows are DocCoverage, and summing them keeps the
// roll-up one screen whatever the corpus size.
type Overview struct {
	Violations  int `json:"violations"`
	StaleIntent int `json:"stale_intent"`
	Gaps        int `json:"gaps"`
	// Unimplemented counts accepted sections no component claims;
	// SectionsCovered/SectionsTotal are the corpus-wide coverage ratio over
	// non-superseded sections.
	Unimplemented   int           `json:"unimplemented"`
	SectionsCovered int           `json:"sections_covered"`
	SectionsTotal   int           `json:"sections_total"`
	StaleClaims     int           `json:"stale_claims"`
	OrphanedClaims  int           `json:"orphaned_claims"`
	FrontierSize    int           `json:"frontier_size"`
	Cycles          [][]string    `json:"cycles,omitempty"`
	CriticalHead    *FrontierTask `json:"critical_head,omitempty"`
	GraphEnabled    bool          `json:"graph_enabled"`
}

// DeriveResult reports one deriver run (spec 007). internal/derive aliases
// this as derive.Result; the shape is declared here because a handler
// serializes it (ADR 036 §2).
type DeriveResult struct {
	Graph   string `json:"graph"`
	Hash    string `json:"hash"`
	Skipped bool   `json:"skipped"`
	Bytes   int    `json:"bytes"`
	// Empty reports that the deriver produced no triples at all. Legitimate
	// for some sources (worklode's own go-imports: one whole-repo component,
	// so every import edge is intra-component and dropped by design), and the
	// signature of a broken input for the rest — so it is reported, never
	// inferred from Bytes by each caller.
	Empty bool `json:"empty"`
}

// DeriveResponse is the response envelope of a deriver run.
type DeriveResponse struct {
	Results []DeriveResult `json:"results"`
}
