package progress

import (
	"fmt"
	"time"
)

// PositionFacts is one task's run-board facts (032 §8), typed so Position
// stays pure: no store, no HTTP, no clock reads beyond the Now field a
// caller fills.
type PositionFacts struct {
	State    string
	Assignee string
	Lease    *LeaseFact // nil when none live
	PR       *PRFact    // nil when no open PR carries the task
	CI       *CIFact    // latest run for PR.HeadSHA, nil when none
	Queued   bool       // part 4 sets this; false until then
	Now      time.Time
}

// LeaseFact is a live lease on a task (004 §5).
type LeaseFact struct {
	Actor string
	Since time.Time
}

// PRFact is the open pull request carrying a task.
type PRFact struct {
	Number int
	URL    string
}

// CIFact is the latest CI run for a PR's head SHA.
type CIFact struct {
	Status     string
	Conclusion string
}

// deliveryText renders TaskClass's "landed" states per §2.4's ladder.
var deliveryText = map[string]string{
	"merged":        "merged",
	"deployed_dev":  "deployed to dev",
	"deployed_prod": "deployed to prod",
	"released":      "released",
}

// Position renders a task's position on WL-SPEC-66 §2.4's ladder: the first
// rung that applies wins. A landed state (TaskClass) always wins over any
// PR or CI fact, so a stale PR row on a merged task can't outrank the
// delivery state that already superseded it.
func Position(f PositionFacts) string {
	if TaskClass(f.State) == "landed" {
		if text, ok := deliveryText[f.State]; ok {
			return text
		}
	}
	switch {
	case f.Queued:
		return "queued for merge"
	case f.PR != nil && f.CI != nil && f.CI.Conclusion == "failure":
		return fmt.Sprintf("checks failed on PR #%d", f.PR.Number)
	case f.PR != nil && f.CI != nil && (f.CI.Status == "in_progress" || f.CI.Status == "queued"):
		return fmt.Sprintf("checks running on PR #%d", f.PR.Number)
	case f.PR != nil:
		return fmt.Sprintf("PR #%d open", f.PR.Number)
	case f.State == "in_review":
		return "in review"
	case f.State == "in_progress" && f.Lease != nil:
		return fmt.Sprintf("claimed by %s, %s", f.Lease.Actor, humanAge(f.Now.Sub(f.Lease.Since)))
	case f.State == "ready" && f.Assignee != "":
		return fmt.Sprintf("assigned to %s", f.Assignee)
	case f.State == "ready":
		return "ready"
	default:
		return f.State
	}
}

// humanAge renders a duration in the coarsest single unit that stays
// legible: minutes under an hour, hours under a day, days beyond that
// ("4m", "2h", "3d").
func humanAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}
