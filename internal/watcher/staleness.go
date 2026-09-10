package watcher

import (
	"fmt"
	"time"
)

// TypeDocStale is events.type of the sweeper crossing a document into the
// stale status (025 §8.7). Like TypeDocPatched, it is a dotted backbone type
// with no ns/ mirror: the sweeper that emits it and the rule below that
// consumes it both live outside this package's purity boundary (025 §19).
const TypeDocStale = "doc.stale"

// ruleGroomOnStale is the "rule" metric label for the §8.7 groom mint.
const ruleGroomOnStale = "groom-on-stale"

// StaleInput is everything the §8.7 clock consults for one document.
type StaleInput struct {
	DocKind      string // spec | adr | plan
	Status       string
	UpdatedAt    time.Time // any revision or patch re-arms the clock
	ProjectDays  int       // projects.doc_staleness_days; 0 = no override
	DefaultDays  int       // instance default (30)
	HasExecution bool      // plan: a task ever leased; spec: an accepted covering plan
}

// StaleAt returns the instant the document crosses the staleness threshold,
// or the zero time when it never does: not accepted, an ADR (decisions
// already taken are never groomed), or execution exists. The threshold is
// inclusive — the document counts as stale at exactly UpdatedAt plus the
// threshold, not only strictly after it, so a sweeper comparing to "now"
// should test !now.Before(StaleAt(...)).
func StaleAt(in StaleInput) time.Time {
	if in.DocKind == "adr" {
		return time.Time{}
	}
	if in.Status != "accepted" {
		return time.Time{}
	}
	if in.HasExecution {
		return time.Time{}
	}
	days := in.ProjectDays
	if days == 0 {
		days = in.DefaultDays
	}
	return in.UpdatedAt.AddDate(0, 0, days)
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
