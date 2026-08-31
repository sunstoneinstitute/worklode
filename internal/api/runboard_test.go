package api

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func runGroupFact(state string, lease *store.Lease, blockers []store.TaskRef) store.ProjectWorkFact {
	return store.ProjectWorkFact{
		Task:         model.Task{State: state},
		Lease:        lease,
		OpenBlockers: blockers,
	}
}

func TestRunGroupOf(t *testing.T) {
	lease := &store.Lease{}
	blocker := []store.TaskRef{{ID: "WL-1"}}
	cases := []struct {
		name string
		fact store.ProjectWorkFact
		want runGroup
	}{
		{"ready unblocked", runGroupFact("ready", nil, nil), runGroupReady},
		{"ready blocked", runGroupFact("ready", nil, blocker), runGroupWaiting},
		{"in_progress leased", runGroupFact("in_progress", lease, nil), runGroupRunning},
		{"in_progress orphaned", runGroupFact("in_progress", nil, nil), runGroupJudgment},
		{"in_review", runGroupFact("in_review", nil, nil), runGroupJudgment},
		{"in_review blocked still judgment", runGroupFact("in_review", nil, blocker), runGroupJudgment},
		{"abandoned", runGroupFact("abandoned", nil, nil), runGroupFailed},
		{"merged", runGroupFact("merged", nil, nil), runGroupCompleted},
		{"deployed_prod", runGroupFact("deployed_prod", nil, nil), runGroupCompleted},
		{"released", runGroupFact("released", nil, nil), runGroupCompleted},
		{"draft excluded", runGroupFact("draft", nil, nil), runGroupNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runGroupOf(tc.fact); got != tc.want {
				t.Errorf("runGroupOf(%q) = %v, want %v", tc.fact.Task.State, got, tc.want)
			}
		})
	}
}
