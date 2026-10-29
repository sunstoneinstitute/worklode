package model

import "time"

// StageDecision is one recorded stage transition, oldest first.
type StageDecision struct {
	Stage     string    `json:"stage"`  // research | report | story | distribution
	Reason    string    `json:"reason"` // the decision's notes
	DecidedAt time.Time `json:"decided_at"`
}

// StageView is the derived stage (WL-REQ-119). Stage is "" before Research is
// entered. Carryover lists the ids of open tasks created before the latest
// transition. RecommendClosure is a recommendation the lead confirms.
type StageView struct {
	Stage            string          `json:"stage"`
	History          []StageDecision `json:"history"`
	Carryover        []string        `json:"carryover"`
	RecommendClosure bool            `json:"recommend_closure"`
}
