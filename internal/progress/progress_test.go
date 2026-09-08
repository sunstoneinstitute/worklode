package progress

import (
	"reflect"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestPlanState(t *testing.T) {
	landed := Task{State: "merged"}
	unstarted := Task{State: "ready"}
	active := Task{State: "in_progress"}
	cases := []struct {
		name, status string
		tasks        []Task
		want         string
	}{
		{"draft", "draft", []Task{landed}, "draft"},
		{"superseded is spent", "superseded", nil, "built"},
		{"accepted, all landed", "accepted", []Task{landed, landed}, "built"},
		{"accepted, one unstarted", "accepted", []Task{landed, unstarted}, "in_progress"},
		{"accepted, active only", "accepted", []Task{active}, "in_progress"},
		{"accepted, none moved", "accepted", []Task{unstarted, unstarted}, "not_started"},
		{"accepted, no tasks", "accepted", nil, "no_record"},
		{"abandoned ignored", "accepted", []Task{landed, {State: "abandoned"}}, "built"},
		{"only abandoned is no record", "accepted", []Task{{State: "abandoned"}}, "no_record"},
	}
	for _, c := range cases {
		if got := PlanState(c.status, c.tasks); got != c.want {
			t.Errorf("%s: PlanState = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestTaskClassPartitionsTaskStates pins today's membership and is what
// makes a new state fail the build until someone names its class.
func TestTaskClassPartitionsTaskStates(t *testing.T) {
	want := map[string]string{
		"draft": "unstarted", "ready": "unstarted",
		"in_progress": "active", "in_review": "active",
		"merged": "landed", "deployed_dev": "landed",
		"deployed_prod": "landed", "released": "landed",
		"abandoned": "abandoned",
	}
	if len(want) != len(model.TaskStates) {
		t.Fatalf("table has %d states, model.TaskStates has %d: name the new state's class", len(want), len(model.TaskStates))
	}
	for _, st := range model.TaskStates {
		if got := TaskClass(st); got != want[st] {
			t.Errorf("TaskClass(%q) = %q, want %q", st, got, want[st])
		}
	}
}

// TestSectionState covers §1.2's table, plus the three extra rules the
// table's rows don't spell out on their own.
func TestSectionState(t *testing.T) {
	cases := []struct {
		name        string
		covers      []CoverState
		wantState   string
		wantPartial bool
	}{
		{
			"built: best covering plan is built",
			[]CoverState{{Cover: Cover{Level: "full"}, State: "built"}},
			"built", false,
		},
		{
			"in_progress: best covering plan is in_progress",
			[]CoverState{{Cover: Cover{Level: "full"}, State: "in_progress"}},
			"in_progress", false,
		},
		{
			"not_started: best covering plan is not_started",
			[]CoverState{{Cover: Cover{Level: "full"}, State: "not_started"}},
			"not_started", false,
		},
		{
			"no_record: best covering plan is no_record",
			[]CoverState{{Cover: Cover{Level: "full"}, State: "no_record"}},
			"no_record", false,
		},
		{
			"draft: every covering plan is draft",
			[]CoverState{
				{Cover: Cover{Level: "full"}, State: "draft"},
				{Cover: Cover{Level: "full"}, State: "draft"},
			},
			"draft", false,
		},
		{
			"unplanned: no plan covers the section",
			nil,
			"unplanned", false,
		},
		{
			"bound: every cover is at coverage none",
			[]CoverState{{Cover: Cover{Level: "none"}, State: "built"}},
			"bound", false,
		},
		{
			"partial-only covers set the flag",
			[]CoverState{{Cover: Cover{Level: "partial"}, State: "in_progress"}},
			"in_progress", true,
		},
		{
			"a none cover beside a full one is ignored",
			[]CoverState{
				{Cover: Cover{Level: "none"}, State: "built"},
				{Cover: Cover{Level: "full"}, State: "in_progress"},
			},
			"in_progress", false,
		},
		{
			"draft beside built is built",
			[]CoverState{
				{Cover: Cover{Level: "full"}, State: "draft"},
				{Cover: Cover{Level: "full"}, State: "built"},
			},
			"built", false,
		},
	}
	for _, c := range cases {
		state, partial := SectionState(c.covers)
		if state != c.wantState || partial != c.wantPartial {
			t.Errorf("%s: SectionState = (%q, %v), want (%q, %v)",
				c.name, state, partial, c.wantState, c.wantPartial)
		}
	}
}

// findGroup returns the specs of the named group ("active" | "planning" |
// "no_record" | "built"), failing the test if the group is missing — Derive
// always emits all four, in §1.3 order.
func findGroup(t *testing.T, pp model.ProjectProgress, key string) []model.ProgressSpec {
	t.Helper()
	for _, g := range pp.Groups {
		if g.Key == key {
			return g.Specs
		}
	}
	t.Fatalf("no group %q in %+v", key, pp.Groups)
	return nil
}

// TestGroupAndNextAct drives Derive end to end, one scenario per group
// condition and per next-act rule (§1.3), checking that a spec lands in the
// expected group with the expected act text.
func TestGroupAndNextAct(t *testing.T) {
	cases := []struct {
		name      string
		in        Input
		wantGroup string
		wantKind  string
		wantText  string
		wantPlans []string
	}{
		{
			name: "one open plan: active group, open-task act",
			in: Input{
				Specs: []Spec{{Doc: 1, Ref: "WL-SPEC-1", Sections: []Section{{Anchor: "sec-1"}}}},
				Plans: []Plan{{
					Doc: 1, Ref: "WL-PLAN-1", Status: "accepted",
					Covers: []Cover{{Spec: 1, Anchor: "sec-1", Level: "full"}},
					Tasks:  []Task{{State: "merged"}, {State: "ready"}},
				}},
			},
			wantGroup: "active", wantKind: "in_progress",
			wantText: "1 open task(s) in WL-PLAN-1", wantPlans: []string{"WL-PLAN-1"},
		},
		{
			name: "five open plans: active group, act names all five",
			in: Input{
				Specs: []Spec{{Doc: 2, Ref: "WL-SPEC-2", Sections: []Section{
					{Anchor: "sec-1"}, {Anchor: "sec-2"}, {Anchor: "sec-3"}, {Anchor: "sec-4"}, {Anchor: "sec-5"},
				}}},
				Plans: []Plan{
					{Doc: 10, Ref: "WL-PLAN-10", Status: "accepted", Covers: []Cover{{Spec: 2, Anchor: "sec-1", Level: "full"}}, Tasks: []Task{{State: "ready"}, {State: "ready"}}},
					{Doc: 11, Ref: "WL-PLAN-11", Status: "accepted", Covers: []Cover{{Spec: 2, Anchor: "sec-2", Level: "full"}}, Tasks: []Task{{State: "ready"}, {State: "ready"}}},
					{Doc: 12, Ref: "WL-PLAN-12", Status: "accepted", Covers: []Cover{{Spec: 2, Anchor: "sec-3", Level: "full"}}, Tasks: []Task{{State: "ready"}, {State: "ready"}}},
					{Doc: 13, Ref: "WL-PLAN-13", Status: "accepted", Covers: []Cover{{Spec: 2, Anchor: "sec-4", Level: "full"}}, Tasks: []Task{{State: "ready"}, {State: "ready"}}},
					{Doc: 14, Ref: "WL-PLAN-14", Status: "accepted", Covers: []Cover{{Spec: 2, Anchor: "sec-5", Level: "full"}}, Tasks: []Task{{State: "ready"}, {State: "ready"}}},
				},
			},
			wantGroup: "active", wantKind: "in_progress",
			wantText:  "10 open task(s) in WL-PLAN-10, WL-PLAN-11, WL-PLAN-12, WL-PLAN-13, WL-PLAN-14",
			wantPlans: []string{"WL-PLAN-10", "WL-PLAN-11", "WL-PLAN-12", "WL-PLAN-13", "WL-PLAN-14"},
		},
		{
			name: "two draft plans: planning group, accept act names both",
			in: Input{
				Specs: []Spec{{Doc: 3, Ref: "WL-SPEC-3", Sections: []Section{{Anchor: "sec-1"}, {Anchor: "sec-2"}}}},
				Plans: []Plan{
					{Doc: 20, Ref: "WL-PLAN-20", Status: "draft", Covers: []Cover{{Spec: 3, Anchor: "sec-1", Level: "full"}}},
					{Doc: 21, Ref: "WL-PLAN-21", Status: "draft", Covers: []Cover{{Spec: 3, Anchor: "sec-2", Level: "full"}}},
				},
			},
			wantGroup: "planning", wantKind: "draft",
			wantText: "accept WL-PLAN-20, WL-PLAN-21", wantPlans: []string{"WL-PLAN-20", "WL-PLAN-21"},
		},
		{
			name: "no plan at all: planning group, unplanned act",
			in: Input{
				Specs: []Spec{{Doc: 4, Ref: "WL-SPEC-4", Sections: []Section{{Anchor: "sec-1"}}}},
			},
			wantGroup: "planning", wantKind: "unplanned",
			wantText: "1 section(s) unplanned",
		},
		{
			name: "accepted plan with no minted tasks: no_record group and act",
			in: Input{
				Specs: []Spec{{Doc: 5, Ref: "WL-SPEC-5", Sections: []Section{{Anchor: "sec-1"}}}},
				Plans: []Plan{{
					Doc: 30, Ref: "WL-PLAN-30", Status: "accepted",
					Covers: []Cover{{Spec: 5, Anchor: "sec-1", Level: "full"}},
				}},
			},
			wantGroup: "no_record", wantKind: "no_record",
			wantText: "no execution record for WL-PLAN-30", wantPlans: []string{"WL-PLAN-30"},
		},
		{
			name: "everything landed: built group, nothing outstanding",
			in: Input{
				Specs: []Spec{{Doc: 6, Ref: "WL-SPEC-6", Sections: []Section{{Anchor: "sec-1"}}}},
				Plans: []Plan{{
					Doc: 40, Ref: "WL-PLAN-40", Status: "accepted",
					Covers: []Cover{{Spec: 6, Anchor: "sec-1", Level: "full"}},
					Tasks:  []Task{{State: "merged"}},
				}},
			},
			wantGroup: "built", wantKind: "none",
			wantText: "nothing outstanding",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pp := Derive(c.in)
			specs := findGroup(t, pp, c.wantGroup)
			if len(specs) != 1 {
				t.Fatalf("group %q has %d specs, want 1: %+v", c.wantGroup, len(specs), specs)
			}
			got := specs[0].Next
			if got.Kind != c.wantKind || got.Text != c.wantText || !reflect.DeepEqual(got.Plans, plansOrNil(c.wantPlans)) {
				t.Errorf("Next = %+v, want {Kind:%q Text:%q Plans:%v}", got, c.wantKind, c.wantText, c.wantPlans)
			}
		})
	}
}

// plansOrNil normalizes an empty want-slice to nil, matching how Derive
// leaves ProgressAct.Plans unset when a rule names no plan.
func plansOrNil(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// TestDeriveBar checks the section bar: fixed state order, bound sections
// excluded entirely, and a state with no owed sections omitted rather than
// rendered as a zero-count slice.
func TestDeriveBar(t *testing.T) {
	in := Input{
		Specs: []Spec{{
			Doc: 1, Ref: "WL-SPEC-1",
			Sections: []Section{
				{Anchor: "built-1"}, {Anchor: "built-2"},
				{Anchor: "in-progress-1"}, {Anchor: "not-started-1"},
				{Anchor: "no-record-1"}, {Anchor: "bound-1"}, {Anchor: "unplanned-1"},
			},
		}},
		Plans: []Plan{
			{Doc: 1, Ref: "WL-PLAN-1", Status: "accepted", Tasks: []Task{{State: "merged"}}, Covers: []Cover{
				{Spec: 1, Anchor: "built-1", Level: "full"},
				{Spec: 1, Anchor: "built-2", Level: "full"},
			}},
			{Doc: 2, Ref: "WL-PLAN-2", Status: "accepted", Tasks: []Task{{State: "merged"}, {State: "ready"}}, Covers: []Cover{
				{Spec: 1, Anchor: "in-progress-1", Level: "full"},
			}},
			{Doc: 3, Ref: "WL-PLAN-3", Status: "accepted", Tasks: []Task{{State: "ready"}}, Covers: []Cover{
				{Spec: 1, Anchor: "not-started-1", Level: "full"},
			}},
			{Doc: 4, Ref: "WL-PLAN-4", Status: "accepted", Covers: []Cover{
				{Spec: 1, Anchor: "no-record-1", Level: "full"},
			}},
			{Doc: 5, Ref: "WL-PLAN-5", Status: "accepted", Tasks: []Task{{State: "merged"}}, Covers: []Cover{
				{Spec: 1, Anchor: "bound-1", Level: "none"},
			}},
		},
	}
	pp := Derive(in)
	want := []model.ProgressSlice{
		{State: "built", Count: 2},
		{State: "in_progress", Count: 1},
		{State: "not_started", Count: 1},
		{State: "no_record", Count: 1},
		{State: "unplanned", Count: 1},
	}
	if !reflect.DeepEqual(pp.Bar, want) {
		t.Errorf("Bar = %+v, want %+v", pp.Bar, want)
	}
}

// TestDeriveSort checks that specs within a group sort by Updated,
// descending, regardless of input order.
func TestDeriveSort(t *testing.T) {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	in := Input{
		Specs: []Spec{
			{Doc: 1, Ref: "A", Updated: t0},
			{Doc: 2, Ref: "B", Updated: t0.Add(48 * time.Hour)},
			{Doc: 3, Ref: "C", Updated: t0.Add(24 * time.Hour)},
		},
	}
	pp := Derive(in)
	specs := findGroup(t, pp, "built")
	var got []string
	for _, s := range specs {
		got = append(got, s.Ref)
	}
	want := []string{"B", "C", "A"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("group order = %v, want %v", got, want)
	}
}
