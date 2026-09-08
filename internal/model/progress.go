package model

import "time"

// ProjectProgress is WL-SPEC-66 §1 derived for one project: what each spec
// says, and how much of it exists. Nothing here is stored; a reader
// recomputes it per request.
type ProjectProgress struct {
	Project string          `json:"project"`
	Rally   *RallyBand      `json:"rally,omitempty"` // the active rally, if any (§2.1)
	Counts  ProgressCounts  `json:"counts"`
	Bar     []ProgressSlice `json:"bar"`    // owed sections by state, in state order
	Groups  []ProgressGroup `json:"groups"` // §1.3 order: active, planning, no_record, built
}

type RallyBand struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Members int    `json:"members"`
	Landed  int    `json:"landed"`
}

type ProgressCounts struct {
	Active   int `json:"active"`
	Planning int `json:"planning"`
	NoRecord int `json:"no_record"`
	Built    int `json:"built"`
}

type ProgressSlice struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

type ProgressGroup struct {
	Key   string         `json:"key"` // active | planning | no_record | built
	Specs []ProgressSpec `json:"specs"`
}

type ProgressSpec struct {
	Doc     int64     `json:"doc"`
	Ref     string    `json:"ref"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated_at"`
	// Status and Owner are the document's own, not derived: §3.2's Accept
	// button is offered on a draft spec and enabled only for its owner.
	Status string `json:"status"`
	Owner  string `json:"owner,omitempty"`
	// PlanningTask is the open design task about this spec (025 §15.4's
	// planning task), when one exists. The row shows it as a link instead of
	// §3.4's Plan button, which mints exactly that task.
	PlanningTask string            `json:"planning_task,omitempty"`
	Group        string            `json:"group"`
	Next         ProgressAct       `json:"next"`
	Sections     []ProgressSection `json:"sections"`
	Plans        []ProgressPlan    `json:"plans"`
}

// ProgressAct is §1.3's next act: Kind names the state that decides it and
// Text is the rendered line, plans never shortened.
type ProgressAct struct {
	Kind  string   `json:"kind"`
	Text  string   `json:"text"`
	Plans []string `json:"plans,omitempty"`
}

type ProgressSection struct {
	Anchor  string   `json:"anchor"`
	Heading string   `json:"heading"`
	Depth   int      `json:"depth"`
	State   string   `json:"state"` // built | in_progress | not_started | no_record | draft | unplanned | bound
	Partial bool     `json:"partial"`
	Plans   []string `json:"plans"` // refs of covering plans, level none excluded
}

type ProgressPlan struct {
	Doc      int64          `json:"doc"`
	Ref      string         `json:"ref"`
	Title    string         `json:"title"`
	Status   string         `json:"status"` // draft | accepted | superseded
	Owner    string         `json:"owner,omitempty"`
	State    string         `json:"state"` // draft | built | in_progress | not_started | no_record
	Requires []string       `json:"requires"`
	Tasks    []ProgressTask `json:"tasks"`
	Landed   int            `json:"landed"`
	Open     int            `json:"open"` // active plus unstarted
}

type ProgressTask struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	State    string `json:"state"`
	Class    string `json:"class"`    // landed | active | unstarted (§1.1)
	Position string `json:"position"` // §2.4's ladder, one line
}

// ProgressAcceptInput is the body POST /projects/{id}/progress/accept takes
// (WL-SPEC-66 §3.2): the document to accept, and nothing else. The acting
// actor is the session's, never the body's (§4.2 rule 6), and the write gate
// refuses a body that names one.
type ProgressAcceptInput struct {
	Doc int64 `json:"doc"`
}

// ProgressAcceptResponse is the reply to POST /projects/{id}/progress/accept
// (WL-SPEC-66 §3.2): the document that was accepted, the status it now
// carries, and how many tasks the acceptance minted (025 §9.2 — zero for a
// spec or ADR). The page applies none of it; it re-reads the row from the
// backbone (§3.1).
type ProgressAcceptResponse struct {
	Doc    int64  `json:"doc"`
	Status string `json:"status"`
	Minted int    `json:"minted"`
}

// ProgressPlanInput is the body POST /projects/{id}/progress/plan takes
// (WL-SPEC-66 §3.4): the spec to mint a planning task for. Like every other
// act on this page, the acting actor is the session's (§4.2 rule 6).
type ProgressPlanInput struct {
	Doc int64 `json:"doc"`
}

// ProgressPlanResponse is the reply to POST /projects/{id}/progress/plan: the
// planning task about the spec. Existing is true when an open one was already
// there and nothing was minted — repeating the request returns the same task
// rather than a second one (§4.2 rule 7).
type ProgressPlanResponse struct {
	Task     string `json:"task"`
	Existing bool   `json:"existing"`
}
