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
