package model

import "time"

// ProjectProgress is WL-REQ-1335 derived for one project: what each spec
// says, and how much of it exists. Nothing here is stored; a reader
// recomputes it per request.
type ProjectProgress struct {
	Project string          `json:"project"`
	Rally   *RallyBand      `json:"rally,omitempty"` // the active rally, if any (WL-REQ-1336)
	Draft   *RallyBand      `json:"draft,omitempty"` // the draft rally the footer offers, if any (WL-REQ-1784)
	Counts  ProgressCounts  `json:"counts"`
	Bar     []ProgressSlice `json:"bar"`    // owed sections by state, in state order
	Groups  []ProgressGroup `json:"groups"` // WL-REQ-1335 order: active, planning, no_record, built
}

// RallyBand is one rally reduced to what a fixed-height bar can show. The
// active rally fills WL-REQ-1336's band, where Landed is the number that moves;
// the draft rally fills WL-REQ-1784's footer, where Specs is — "Rally: N tasks
// from M specs". Each reads the field the other leaves at zero.
type RallyBand struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Members int    `json:"members"`
	Landed  int    `json:"landed"`
	// Specs is how many specs the members come from: the distinct specs of
	// their plans, plus the specs they are directly about. Zero on the band.
	Specs int `json:"specs,omitempty"`
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
	// Status and Owner are the document's own, not derived: WL-REQ-1783's
	// Accept button is offered on a draft spec and enabled only for its
	// owner.
	Status string `json:"status"`
	Owner  string `json:"owner,omitempty"`
	// PlanningTask is the open design task about this spec (WL-RULE-179's
	// planning task), when one exists. The row shows it as a link instead of
	// WL-REQ-1783's Plan button, which mints exactly that task.
	PlanningTask string            `json:"planning_task,omitempty"`
	Group        string            `json:"group"`
	Next         ProgressAct       `json:"next"`
	Sections     []ProgressSection `json:"sections"`
	Plans        []ProgressPlan    `json:"plans"`
}

// ProgressAct is WL-REQ-1335's next act: Kind names the state that decides it
// and Text is the rendered line, plans never shortened.
type ProgressAct struct {
	Kind  string   `json:"kind"`
	Text  string   `json:"text"`
	Plans []string `json:"plans,omitempty"`
}

type ProgressSection struct {
	Anchor  string   `json:"anchor"`
	Heading string   `json:"heading"`
	Depth   int      `json:"depth"`
	State   string   `json:"state"` // built | in_progress | not_started | no_record | draft | unplanned
	Plans   []string `json:"plans"` // refs of covering plans
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
	Class    string `json:"class"`    // landed | active | unstarted (WL-REQ-1335)
	Position string `json:"position"` // WL-REQ-1782's ladder, one line
	// Merge is the open pull request WL-REQ-1785's button acts on, nil when
	// the task has none.
	Merge *ProgressMerge `json:"merge,omitempty"`
}

// ProgressMerge is what the Progress page needs to offer WL-REQ-1785's act on a
// task's open pull request: which PR it is, whether its base branch runs a
// merge queue (WL-REQ-1340, so the button says "Queue for merge" rather than
// "Merge"), and the reason the act cannot be taken now. An empty Reason
// means the button is live.
type ProgressMerge struct {
	Repo   string `json:"repo"`
	Number int64  `json:"number"`
	Queue  bool   `json:"queue"`
	Reason string `json:"reason,omitempty"`
}

// ProgressAcceptInput is the body POST /projects/{id}/progress/accept takes
// (WL-REQ-1783): the document to accept, and nothing else. The acting actor is
// the session's, never the body's (WL-REQ-1338 rule 6), and the write gate
// refuses a body that names one.
type ProgressAcceptInput struct {
	Doc int64 `json:"doc"`
}

// ProgressAcceptResponse is the reply to POST /projects/{id}/progress/accept
// (WL-REQ-1783): the document that was accepted, the status it now
// carries, and how many tasks the acceptance minted (WL-REQ-172 — zero for a
// spec or ADR). The page applies none of it; it re-reads the row from the
// backbone (WL-REQ-1337).
type ProgressAcceptResponse struct {
	Doc    int64  `json:"doc"`
	Status string `json:"status"`
	Minted int    `json:"minted"`
}

// ProgressPlanInput is the body POST /projects/{id}/progress/plan takes
// (WL-REQ-1783): the spec to mint a planning task for. Like every other
// act on this page, the acting actor is the session's (WL-REQ-1338 rule 6).
type ProgressPlanInput struct {
	Doc int64 `json:"doc"`
}

// ProgressPlanResponse is the reply to POST /projects/{id}/progress/plan: the
// planning task about the spec. Existing is true when an open one was already
// there and nothing was minted — repeating the request returns the same task
// rather than a second one (WL-REQ-1338 rule 7).
type ProgressPlanResponse struct {
	Task     string `json:"task"`
	Existing bool   `json:"existing"`
}

// ProgressRallyAddInput is the body POST /projects/{id}/progress/rally/add
// takes (WL-REQ-1783): the spec whose remaining work joins the draft
// rally. The acting actor is the session's (WL-REQ-1338 rule 6).
type ProgressRallyAddInput struct {
	Doc int64 `json:"doc"`
}

// ProgressRallyAddResponse is the reply to one add: the draft rally, how many
// members this call was the first to add, and what the footer now says.
// Added is 0 with a 200 when the spec had nothing outstanding (WL-REQ-1784).
type ProgressRallyAddResponse struct {
	Rally   string `json:"rally"`
	Added   int    `json:"added"`
	Members int    `json:"members"`
	Specs   int    `json:"specs"`
}

// ProgressRallyResponse is the reply to Confirm Rally and to Discard: the
// rally that moved and the state it now holds ("ready" or "abandoned").
type ProgressRallyResponse struct {
	Rally string `json:"rally"`
	State string `json:"state"`
}

// ProgressRallyConflict is the 409 Confirm Rally answers when the project
// already has an active rally: the reason the footer shows, and the rally
// that holds the slot, so the page can name it without a second read.
type ProgressRallyConflict struct {
	Error  string `json:"error"`
	Active string `json:"active"`
}

// ProgressMergeInput is the body POST /projects/{id}/progress/merge takes
// (WL-REQ-1783): the task whose pull request to act on, and the PR
// itself. Both are named so the route can refuse a page whose facts have
// gone stale — a PR that no longer carries the task is a 409, not a merge.
// The acting actor is the session's (WL-REQ-1338 rule 6).
type ProgressMergeInput struct {
	Task string     `json:"task"`
	PR   ProgressPR `json:"pr"`
}

// ProgressPR names one pull request by repository and number.
type ProgressPR struct {
	Repo   string `json:"repo"`
	Number int64  `json:"number"`
}

// ProgressMergeResponse is the reply to one merge act: which operation the
// branch's rules called for ("enqueue" or "merge"), and whether the PR is
// now in a merge queue. The page applies neither — the position line changes
// on the stream event that follows (WL-REQ-1785).
type ProgressMergeResponse struct {
	Op     string `json:"op"`
	Queued bool   `json:"queued"`
}

// ProgressEventFrame is the data: payload of GET /projects/{id}/progress/events
// (WL-REQ-1339): what one backbone event moved, reduced to the fields
// the page redraws on. Event is the event's own type, e.g. "task.transition";
// Task is empty when the touch is about a plan or spec rather than a task, in
// which case Plan or Specs carries the touch instead. No field is omitted:
// the page reads a fixed shape, so an absent task is "" and an absent rally
// is null, never a missing key.
type ProgressEventFrame struct {
	Event string     `json:"event"`
	Task  string     `json:"task"`
	State string     `json:"state"`
	Plan  int64      `json:"plan"`
	Specs []int64    `json:"specs"`
	Rally *RallyBand `json:"rally"`
	At    time.Time  `json:"at"`
}
