package model

import "time"

// EvidenceSummary pairs a short human-readable line with the evidence
// category (declared, user_reported, observed, recommended) it was
// classified into (spec 032).
type EvidenceSummary struct {
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

// CockpitMode is the cockpit's derived lifecycle mode — editorial decision,
// approved launch, or ordinary operations — and the evidence basis for that
// classification. Name is one of the fixed mode names; it is never a stored,
// independently-editable field (spec 032).
type CockpitMode struct {
	Name  string          `json:"name"`
	Basis EvidenceSummary `json:"basis"`
}

// CockpitProject is the project identity shown in the cockpit and the
// project-scoped sidebar.
type CockpitProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

// ActorSummary is an actor's id and display name, the minimal identity the
// cockpit surfaces for a work item's owner or delegate, or a pinned focus
// card's pinner.
type ActorSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Focus is the project's curated "Pinned focus" card (migration 0013): a
// lead-authored note plus who pinned it and when.
type Focus struct {
	ObjectType string        `json:"object_type"`
	ObjectID   string        `json:"object_id"`
	Note       string        `json:"note"`
	PinnedBy   *ActorSummary `json:"pinned_by"`
	PinnedAt   time.Time     `json:"pinned_at"`
}

// DecisionAction is one action a governed decision offers.
type DecisionAction struct {
	Label  string `json:"label"`
	Effect string `json:"effect"`
	URL    string `json:"url"`
}

// EvidenceReference is one linked piece of evidence backing (or
// contradicting) a governed decision.
type EvidenceReference struct {
	Label    string `json:"label"`
	URL      string `json:"url"`
	Category string `json:"category"`
}

// Decision is the project's curated "Next decision" card (migration 0013).
type Decision struct {
	Title            string              `json:"title"`
	Accountable      string              `json:"accountable"`
	Subject          string              `json:"subject"`
	Readiness        string              `json:"readiness"`
	Actions          []DecisionAction    `json:"actions"`
	Evidence         []EvidenceReference `json:"evidence"`
	ContraryEvidence []EvidenceReference `json:"contrary_evidence"`
}

// SecondaryConcern is one non-primary concern surfaced on the cockpit — an
// open blocker task or an unfinished plan ordered before the task's plan.
type SecondaryConcern struct {
	Kind     string          `json:"kind"`
	Title    string          `json:"title"`
	URL      string          `json:"url"`
	Evidence EvidenceSummary `json:"evidence"`
}

// Repository is one project-mapped repo and the evidence behind its declared
// done_state.
type Repository struct {
	Repo           string          `json:"repo"`
	DoneState      string          `json:"done_state"`
	StatusEvidence EvidenceSummary `json:"status_evidence"`
}

// CockpitWorkItem is one task on the cockpit's work list: identity and
// state, plus the resolved owner/delegate and the evidence behind its
// current state.
type CockpitWorkItem struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Priority       string          `json:"priority"`
	State          string          `json:"state"`
	Blocked        bool            `json:"blocked"`
	URL            string          `json:"url"`
	Owner          *ActorSummary   `json:"owner"`
	Delegate       *ActorSummary   `json:"delegate"`
	StatusEvidence EvidenceSummary `json:"status_evidence"`
}

// CockpitWork holds the cockpit's four work buckets.
type CockpitWork struct {
	InProgress []CockpitWorkItem `json:"in_progress"`
	InReview   []CockpitWorkItem `json:"in_review"`
	Ready      []CockpitWorkItem `json:"ready"`
	Blocked    []CockpitWorkItem `json:"blocked"`
}

// CockpitProjection is the wire form of GET /api/v1/projects/{id}/cockpit
// (spec 032): a projection built fresh from declared facts on every call,
// never a stored workflow field.
type CockpitProjection struct {
	CanonicalURL      string             `json:"canonical_url"`
	Project           CockpitProject     `json:"project"`
	Mode              CockpitMode        `json:"mode"`
	PinnedFocus       *Focus             `json:"pinned_focus"`
	RankingFocus      []string           `json:"ranking_focus"`
	NextDecision      *Decision          `json:"next_decision"`
	Work              CockpitWork        `json:"work"`
	SecondaryConcerns []SecondaryConcern `json:"secondary_concerns"`
	Repositories      []Repository       `json:"repositories"`
	Cost              CostReport         `json:"cost"`
}
