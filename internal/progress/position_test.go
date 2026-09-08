package progress

import (
	"testing"
	"time"
)

// TestPosition covers WL-SPEC-66 §2.4's ladder, one case per rung in ladder
// order, plus a case proving a delivery state beats a stale PR fact.
func TestPosition(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		f    PositionFacts
		want string
	}{
		{
			"queued for merge",
			PositionFacts{State: "in_review", Queued: true, Now: now},
			"queued for merge",
		},
		{
			"checks failed on PR",
			PositionFacts{State: "in_review", PR: &PRFact{Number: 42}, CI: &CIFact{Conclusion: "failure"}, Now: now},
			"checks failed on PR #42",
		},
		{
			"checks running on PR",
			PositionFacts{State: "in_review", PR: &PRFact{Number: 42}, CI: &CIFact{Status: "in_progress"}, Now: now},
			"checks running on PR #42",
		},
		{
			"PR open",
			PositionFacts{State: "in_review", PR: &PRFact{Number: 42}, Now: now},
			"PR #42 open",
		},
		{
			"in review",
			PositionFacts{State: "in_review", Now: now},
			"in review",
		},
		{
			"claimed by, with age",
			PositionFacts{
				State: "in_progress",
				Lease: &LeaseFact{Actor: "stig", Since: now.Add(-2 * time.Hour)},
				Now:   now,
			},
			"claimed by stig, 2h",
		},
		{
			"assigned to",
			PositionFacts{State: "ready", Assignee: "stig", Now: now},
			"assigned to stig",
		},
		{
			"ready",
			PositionFacts{State: "ready", Now: now},
			"ready",
		},
		{
			"merged",
			PositionFacts{State: "merged", Now: now},
			"merged",
		},
		{
			"deployed to dev",
			PositionFacts{State: "deployed_dev", Now: now},
			"deployed to dev",
		},
		{
			"deployed to prod",
			PositionFacts{State: "deployed_prod", Now: now},
			"deployed to prod",
		},
		{
			"released",
			PositionFacts{State: "released", Now: now},
			"released",
		},
		{
			"state not on the ladder renders as itself",
			PositionFacts{State: "draft", Now: now},
			"draft",
		},
		{
			"delivery state beats a stale PR fact",
			PositionFacts{State: "merged", PR: &PRFact{Number: 7}, Now: now},
			"merged",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Position(c.f); got != c.want {
				t.Errorf("Position(%+v) = %q, want %q", c.f, got, c.want)
			}
		})
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{4 * time.Minute, "4m"},
		{2 * time.Hour, "2h"},
		{3 * 24 * time.Hour, "3d"},
	}
	for _, c := range cases {
		if got := humanAge(c.d); got != c.want {
			t.Errorf("humanAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestMergeAct covers §3.6's button facts: which PR, queue or plain merge,
// and the reasons worklode can tell before asking GitHub.
func TestMergeAct(t *testing.T) {
	pr := func() *PRFact { return &PRFact{Repo: "acme/app", Number: 7} }
	queued := func() *PRFact { p := pr(); p.MergeQueue = true; return p }
	success := &CIFact{Status: "completed", Conclusion: "success"}
	failed := &CIFact{Status: "completed", Conclusion: "failure"}

	cases := []struct {
		name       string
		facts      PositionFacts
		wantNil    bool
		wantQueue  bool
		wantReason string
	}{
		{name: "no PR, nothing to merge",
			facts: PositionFacts{State: "in_progress"}, wantNil: true},
		{name: "a landed task's PR is history",
			facts: PositionFacts{State: "merged", PR: pr()}, wantNil: true},
		{name: "a queue-protected branch queues, checks or no checks",
			facts: PositionFacts{State: "in_review", PR: queued()}, wantQueue: true},
		{name: "a plain merge waits for green checks",
			facts:      PositionFacts{State: "in_review", PR: pr(), CI: failed},
			wantReason: "checks have not passed"},
		{name: "no CI run at all is not green either",
			facts:      PositionFacts{State: "in_review", PR: pr()},
			wantReason: "checks have not passed"},
		{name: "green checks enable the plain merge",
			facts: PositionFacts{State: "in_review", PR: pr(), CI: success}},
		{name: "a PR already in the queue is not queued twice",
			facts:      PositionFacts{State: "in_review", PR: queued(), Queued: true},
			wantQueue:  true,
			wantReason: "already queued for merge"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergeAct(c.facts)
			if c.wantNil {
				if got != nil {
					t.Fatalf("MergeAct = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("MergeAct = nil, want an act")
			}
			if got.Repo != "acme/app" || got.Number != 7 {
				t.Errorf("MergeAct names %s#%d, want acme/app#7", got.Repo, got.Number)
			}
			if got.Queue != c.wantQueue {
				t.Errorf("Queue = %v, want %v", got.Queue, c.wantQueue)
			}
			if got.Reason != c.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, c.wantReason)
			}
		})
	}
}
