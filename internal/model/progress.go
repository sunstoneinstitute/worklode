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
	Doc      int64             `json:"doc"`
	Ref      string            `json:"ref"`
	Title    string            `json:"title"`
	Updated  time.Time         `json:"updated_at"`
	Group    string            `json:"group"`
	Next     ProgressAct       `json:"next"`
	Sections []ProgressSection `json:"sections"`
	Plans    []ProgressPlan    `json:"plans"`
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
	State    string         `json:"state"`  // draft | built | in_progress | not_started | no_record
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
