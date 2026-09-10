// ladder.go is the ladder's non-escalating rungs (025 §15.5): an executor
// logs a gap without stopping, and a fixer logs starting and finishing the
// design fix a gap or escalation named. Unlike EscalateTask, these move
// nothing else — no lease, no edge, no task state — so each is a single
// RecordEvent call with apply left nil.
package store

import (
	"context"
	"fmt"
	"strings"
)

// GapInput is one gap report: the same information EscalateInput carries
// minus To. A gap recorded without stopping has no escalation target —
// inventing one would corrupt the funnel's label set (025 §15.5).
type GapInput struct {
	TaskID string
	Doc    string // document slug, already resolved by the caller
	Anchor string
	Reason string
}

// RecordGap records task.gap_found. Its external id
// (task.gap_found:<task>:<doc>#<anchor>) makes a retried call a no-op: it
// returns the same inserted=false a redelivered webhook gets from
// RecordEvent, never a second log entry.
func (s *Store) RecordGap(ctx context.Context, in GapInput) (inserted bool, err error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		s.metrics.gap("error")
		return false, fmt.Errorf("gap %s: a reason is required: %w", in.TaskID, ErrInvalidInput)
	}
	payload, err := EventPayload(map[string]any{
		"task": in.TaskID, "doc": in.Doc, "anchor": in.Anchor, "reason": reason,
	})
	if err != nil {
		return false, err
	}
	extID := fmt.Sprintf("task.gap_found:%s:%s#%s", in.TaskID, in.Doc, in.Anchor)
	_, inserted, err = s.RecordEvent(ctx, "cli", extID, "task.gap_found", payload, nil)
	if err != nil {
		s.metrics.gap("error")
		return false, err
	}
	if inserted {
		s.metrics.gap("recorded")
	} else {
		s.metrics.gap("replayed")
	}
	return inserted, nil
}

// FixStartedInput is one "started" report: the fixer takes on a design fix.
type FixStartedInput struct {
	TaskID  string
	Tier    string // "plan" | "spec"
	Doc     string // document slug
	Attempt int    // client-supplied ordinal, default 1
}

// RecordFixStarted records fix.started. Attempt defaults to 1; its external
// id (fix.started:<task>:<n>) is what the matching "finished" call's
// Attempt must repeat to pair with it.
func (s *Store) RecordFixStarted(ctx context.Context, in FixStartedInput) (inserted bool, err error) {
	if in.Tier != "plan" && in.Tier != "spec" {
		s.metrics.fix("started", "error")
		return false, fmt.Errorf("fix started %s: tier must be \"plan\" or \"spec\": %w", in.TaskID, ErrInvalidInput)
	}
	if strings.TrimSpace(in.Doc) == "" {
		s.metrics.fix("started", "error")
		return false, fmt.Errorf("fix started %s: a document is required: %w", in.TaskID, ErrInvalidInput)
	}
	attempt := in.Attempt
	if attempt == 0 {
		attempt = 1
	}
	payload, err := EventPayload(map[string]any{
		"task": in.TaskID, "tier": in.Tier, "doc": in.Doc, "attempt": attempt,
	})
	if err != nil {
		return false, err
	}
	extID := fmt.Sprintf("fix.started:%s:%d", in.TaskID, attempt)
	_, inserted, err = s.RecordEvent(ctx, "cli", extID, "fix.started", payload, nil)
	if err != nil {
		s.metrics.fix("started", "error")
		return false, err
	}
	if inserted {
		s.metrics.fix("started", "recorded")
	} else {
		s.metrics.fix("started", "replayed")
	}
	return inserted, nil
}

// FixFinishedInput is one "finished" report: the fixer's outcome.
type FixFinishedInput struct {
	TaskID  string
	Outcome string // "resolved" | "substantive" | "escalated"
	Attempt int    // must match the "started" call's Attempt to pair with it
}

// RecordFixFinished records fix.finished. Its external id
// (fix.finished:<task>:<n>) uses the same attempt ordinal as the "started"
// call it closes.
func (s *Store) RecordFixFinished(ctx context.Context, in FixFinishedInput) (inserted bool, err error) {
	switch in.Outcome {
	case "resolved", "substantive", "escalated":
	default:
		s.metrics.fix("finished", "error")
		return false, fmt.Errorf(
			"fix finished %s: outcome must be \"resolved\", \"substantive\" or \"escalated\": %w",
			in.TaskID, ErrInvalidInput)
	}
	attempt := in.Attempt
	if attempt == 0 {
		attempt = 1
	}
	payload, err := EventPayload(map[string]any{
		"task": in.TaskID, "outcome": in.Outcome, "attempt": attempt,
	})
	if err != nil {
		return false, err
	}
	extID := fmt.Sprintf("fix.finished:%s:%d", in.TaskID, attempt)
	_, inserted, err = s.RecordEvent(ctx, "cli", extID, "fix.finished", payload, nil)
	if err != nil {
		s.metrics.fix("finished", "error")
		return false, err
	}
	if inserted {
		s.metrics.fix("finished", "recorded")
	} else {
		s.metrics.fix("finished", "replayed")
	}
	return inserted, nil
}
