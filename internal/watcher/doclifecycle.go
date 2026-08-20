// Package watcher implements the doc-lifecycle rules of spec 025 §15.4:
// given one domain event, Evaluate returns the actions it implies. It has
// no store handle and no HTTP (025 §19) — the executor that fetches the
// guard facts (open review/design tasks) and performs the mint lives in
// internal/api. Keeping the rules a pure function makes them table-testable
// without Postgres, and keeps the loop that drives them testable without
// the rules.
package watcher

import (
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
)

// Rule labels Evaluate emits — also the "rule" metric label (§15.7).
const (
	ruleReviewOnSubmit = "review-on-submit"
	rulePlanOnAccept   = "plan-on-accept"
)

// Input is everything the two rules of spec 025 §15.4 may consult. The
// executor fills it; Evaluate never touches the store, so the rules are a
// pure function (025 §19).
type Input struct {
	EventID   int64
	EventType string // events.type: a wl: curie or a vendor dotted type
	DocID     int64
	DocIRI    string
	DocKind   string // spec | adr | plan
	DocTitle  string
	Version   int // document version the event concerns; the review task body names it
	Project   string
	// Open task of the relevant kind already referencing the doc; "" = none.
	OpenReviewTask string
	OpenDesignTask string
}

// Action is one consequence for the executor to perform.
type Action struct {
	Rule       string // "review-on-submit" | "plan-on-accept" — the metric label
	Suppressed bool   // guard hit: perform no mint
	NoteTask   string // when suppressed on accept: note the absorbed event here (§5)
	// Mint parameters (Suppressed == false):
	TaskKind string // "review" | "design"
	Title    string
	Body     string
}

// Evaluate applies the two hardcoded rules of 025 §15.4. Rules must never
// emit an event this subscriber consumes (no cascades — a rule, reviewed
// here, not a mechanism; §5).
func Evaluate(in Input) []Action {
	switch in.EventType {
	case eventbus.TypeDocumentSubmitted:
		return evaluateSubmitted(in)
	case eventbus.TypeDocumentAccepted:
		return evaluateAccepted(in)
	default:
		// Dotted vendor types (push, …) and any wl: curie this subscriber
		// does not know about both fall through here.
		return nil
	}
}

func evaluateSubmitted(in Input) []Action {
	if in.OpenReviewTask != "" {
		// No NoteTask: the event log's own (source, external_id) dedup
		// usually absorbs a same-version resubmit before this guard even
		// runs, so there is rarely a second event to note anywhere (§15.4).
		return []Action{{Rule: ruleReviewOnSubmit, Suppressed: true}}
	}
	return []Action{{
		Rule:     ruleReviewOnSubmit,
		TaskKind: "review",
		Title:    "Review: " + in.DocTitle,
		Body:     reviewBody(in),
	}}
}

func evaluateAccepted(in Input) []Action {
	if in.DocKind != "spec" {
		// 025 §9.2: an accepted plan mints its own task set in the
		// accepting transaction — nothing above it. §15.4's rule is
		// explicitly scoped "where the document is a spec", so ADR
		// acceptance mints nothing either.
		return nil
	}
	if in.OpenDesignTask != "" {
		return []Action{{Rule: rulePlanOnAccept, Suppressed: true, NoteTask: in.OpenDesignTask}}
	}
	return []Action{{
		Rule:     rulePlanOnAccept,
		TaskKind: "design",
		Title:    "Plan: decompose " + in.DocTitle + " into plans",
		Body:     planBody(in),
	}}
}

func reviewBody(in Input) string {
	return fmt.Sprintf(`Review %s (version %d).

prov:wasInformedBy wlid:event/%d

Closing this task is the review outcome. Accepting the document is a
separate, deliberate act — %s — which this task does not perform.`,
		in.DocIRI, in.Version, in.EventID, "`lode doc accept`")
}

func planBody(in Input) string {
	return fmt.Sprintf(`%s (version %d) was accepted.

Decide how to decompose this spec into plans, and write them.

prov:wasInformedBy wlid:event/%d

Claim this task (%s) before writing anything, so this session's
tokens bill to it instead of going unattributed (025 §15.6).`,
		in.DocIRI, in.Version, in.EventID, "`lode task claim <this task's id>`")
}
