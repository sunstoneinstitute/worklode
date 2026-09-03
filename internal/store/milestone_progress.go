package store

import "github.com/sunstoneinstitute/worklode/internal/model"

// liveDeliverableStates are the reported states 029 §2 counts as "live" for
// a milestone's progress bucket. Pinned here only — applied nowhere else.
var liveDeliverableStates = map[string]bool{
	"published": true,
	"updated":   true,
}

// ComputeMilestoneProgress derives 029 §2's milestone progress from the
// children's states. taskStates are the milestone's tasks' State values;
// deliverableStates are its deliverables' ReportedState values ("" when
// nothing has reported). Closed means deliveredStateSet; live means
// published or updated — the buckets are pinned in the milestones plan and
// applied nowhere else.
func ComputeMilestoneProgress(taskStates, deliverableStates []string) model.MilestoneProgress {
	p := model.MilestoneProgress{
		TasksTotal:        len(taskStates),
		DeliverablesTotal: len(deliverableStates),
	}
	for _, st := range taskStates {
		if deliveredStateSet[st] {
			p.TasksClosed++
		}
	}
	for _, st := range deliverableStates {
		if liveDeliverableStates[st] {
			p.DeliverablesLive++
		}
	}
	return p
}
