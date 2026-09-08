package progress

import (
	"reflect"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// plan builds one ProgressPlan with the given state and tasks, so the table
// below reads as the rule it checks rather than as struct literals.
func rallyPlan(ref string, doc int64, status, state string, tasks ...model.ProgressTask) model.ProgressPlan {
	return model.ProgressPlan{Doc: doc, Ref: ref, Status: status, State: state, Tasks: tasks}
}

func rallyTask(id, class string) model.ProgressTask {
	return model.ProgressTask{ID: id, Class: class}
}

// TestRallyMembers covers §3.5's three acts and the exclusions: a no_record
// plan contributes nothing, a landed task is not remaining work, and an act
// whose task already exists is reused rather than minted again.
func TestRallyMembers(t *testing.T) {
	t.Parallel()
	unplanned := []model.ProgressSection{{Anchor: "sec-1", State: "unplanned"}}
	covered := []model.ProgressSection{{Anchor: "sec-1", State: "in_progress"}}

	cases := []struct {
		name  string
		spec  model.ProgressSpec
		facts map[string]PlanFacts
		want  Members
	}{{
		name: "execute: open tasks of an accepted plan, landed ones left out",
		spec: model.ProgressSpec{
			Sections: covered,
			Plans: []model.ProgressPlan{rallyPlan("P-PLAN-1", 1, "accepted", "in_progress",
				rallyTask("WL-1", "landed"), rallyTask("WL-2", "active"), rallyTask("WL-3", "unstarted"))},
		},
		want: Members{Execute: []string{"WL-2", "WL-3"}},
	}, {
		name: "plan: an unplanned section with no planning task",
		spec: model.ProgressSpec{Sections: unplanned},
		want: Members{NeedsPlanning: true},
	}, {
		name: "plan: the open planning task is the member, nothing to mint",
		spec: model.ProgressSpec{Sections: unplanned, PlanningTask: "WL-9"},
		want: Members{Execute: []string{"WL-9"}},
	}, {
		name: "accept: a draft plan with no decision task",
		spec: model.ProgressSpec{
			Sections: covered,
			Plans:    []model.ProgressPlan{rallyPlan("P-PLAN-2", 7, "draft", "draft")},
		},
		want: Members{NeedsAccept: []int64{7}},
	}, {
		name: "accept: the open decision task is the member, nothing to mint",
		spec: model.ProgressSpec{
			Sections: covered,
			Plans:    []model.ProgressPlan{rallyPlan("P-PLAN-2", 7, "draft", "draft")},
		},
		facts: map[string]PlanFacts{"P-PLAN-2": {DecisionTask: "WL-8"}},
		want:  Members{Execute: []string{"WL-8"}},
	}, {
		name: "no_record contributes nothing",
		spec: model.ProgressSpec{
			Sections: covered,
			Plans:    []model.ProgressPlan{rallyPlan("P-PLAN-3", 3, "accepted", "no_record")},
		},
		want: Members{},
	}, {
		name: "a superseded plan is spent, open task or not",
		spec: model.ProgressSpec{
			Sections: covered,
			Plans: []model.ProgressPlan{rallyPlan("P-PLAN-4", 4, "superseded", "built",
				rallyTask("WL-4", "unstarted"))},
		},
		want: Members{},
	}, {
		name: "a built plan owes nothing and a fully covered spec needs no plan",
		spec: model.ProgressSpec{
			Sections: covered,
			Plans: []model.ProgressPlan{rallyPlan("P-PLAN-5", 5, "accepted", "built",
				rallyTask("WL-5", "landed"))},
		},
		want: Members{},
	}, {
		name: "all three acts at once",
		spec: model.ProgressSpec{
			Sections:     append(append([]model.ProgressSection{}, covered...), unplanned...),
			PlanningTask: "",
			Plans: []model.ProgressPlan{
				rallyPlan("P-PLAN-6", 6, "accepted", "in_progress", rallyTask("WL-6", "active")),
				rallyPlan("P-PLAN-7", 7, "draft", "draft"),
			},
		},
		want: Members{Execute: []string{"WL-6"}, NeedsPlanning: true, NeedsAccept: []int64{7}},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RallyMembers(tc.spec, tc.facts)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("RallyMembers = %+v, want %+v", got, tc.want)
			}
		})
	}
}
