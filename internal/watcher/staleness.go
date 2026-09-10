package watcher

import (
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/staleness"
)

// TypeDocStale is events.type of the sweeper crossing a document into the
// stale status (025 §8.7). Like TypeDocPatched, it is a dotted backbone type
// with no ns/ mirror: the sweeper that emits it and the rule below that
// consumes it both live outside this package's purity boundary (025 §19).
const TypeDocStale = "doc.stale"

// ruleGroomOnStale is the "rule" metric label for the §8.7 groom mint.
const ruleGroomOnStale = "groom-on-stale"

// StaleInput is everything the §8.7 clock consults for one document. An
// alias for internal/staleness.Input: the calculation moved to that leaf
// package so internal/store's sweeper can call it without an import cycle
// (internal/watcher imports internal/eventbus, which imports
// internal/store). StaleInput and StaleAt stay here as this package's public
// name for it, since the doc-lifecycle rules (below) are what most callers
// reach for.
type StaleInput = staleness.Input

// StaleAt returns the instant the document crosses the staleness threshold,
// or the zero time when it never does. See internal/staleness.At — the
// threshold is inclusive, so a sweeper comparing to "now" should test
// !now.Before(StaleAt(...)).
func StaleAt(in StaleInput) time.Time {
	return staleness.At(in)
}

// evaluateStale is §8.7's groom mint: a document the sweeper found past its
// staleness threshold owes a decision from whoever wrote it, unless one is
// already pending.
func evaluateStale(in Input) []Action {
	if in.OpenDesignTask != "" {
		return []Action{{Rule: ruleGroomOnStale, Suppressed: true, NoteTask: in.OpenDesignTask}}
	}
	title := "Groom: " + in.DocTitle // spec: re-evaluate, adjust, or close
	if in.DocKind == "plan" {
		title = "Re-plan: " + in.DocTitle // §8.6: regenerated, not patched
	}
	return []Action{{
		Rule:     ruleGroomOnStale,
		TaskKind: "design",
		Title:    title,
		Body:     groomBody(in),
	}}
}

func groomBody(in Input) string {
	why := "no revision or execution since the\nstaleness threshold (025 §8.7)"
	if in.StaleCause == "amended" {
		why = "a spec section it covers was amended in\nplace (025 §8.6)"
	}
	return fmt.Sprintf(`%s has gone stale: %s. The charge is to
"re-evaluate, adjust, or close" it.

prov:wasInformedBy wlid:event/%d

Closing it is %s; do that once you have decided the document no longer earns
its place, not before.`,
		in.DocIRI, why, in.EventID, "`lode doc withdraw`")
}
