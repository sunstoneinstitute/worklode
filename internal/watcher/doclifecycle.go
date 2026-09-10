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
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
)

// TypeDocPatched is events.type of an in-place amendment (025 §8.4). Unlike
// the two wl: curies above it, it is a dotted backbone type with no ns/
// mirror, so it is declared here — beside the rule that consumes it and the
// handler in internal/api that writes it, which imports this name rather
// than repeating the literal.
const TypeDocPatched = "doc.patched"

// Rule labels Evaluate emits — also the "rule" metric label (§15.7).
const (
	ruleReviewOnSubmit   = "review-on-submit"
	rulePlanOnAccept     = "plan-on-accept"
	ruleReviewOnPatch    = "review-on-patch"
	ruleApprovalOnSubmit = "approval-on-submit"
)

// Input is everything the rules of spec 025 §15.4 and §7.3 may consult. The
// executor fills it; Evaluate never touches the store, so the rules are a
// pure function (025 §19).
type Input struct {
	EventID   int64
	EventType string // events.type: a wl: curie or a vendor dotted type
	DocID     int64
	DocIRI    string
	DocKind   string // spec | adr | plan
	DocTitle  string
	DocRef    string // the citable id ("WL-SPEC-25"), which task titles name
	Version   int    // document version the event concerns; the review task body names it
	Project   string
	// Open task of the relevant kind already referencing the doc; "" = none.
	OpenReviewTask string
	OpenDesignTask string
	// OpenApprovalBound says an open approvals row already binds this
	// document at Version — approval-on-submit's guard, filled by the
	// executor the way OpenReviewTask is.
	OpenApprovalBound bool
	// Classification and ChangedAnchors describe a doc.patched event: the
	// caller's §8.4 judgment ("substantive" | "non-substantive") and the
	// anchors the amendment moved. Empty for every other event type.
	Classification string
	ChangedAnchors []string
	// StaleCause is a doc.stale event's payload cause: "amended" when a
	// covered spec section moved under the plan (§8.6), empty for the idle
	// sweeper (§8.7). One rule mints for both; only the body's first
	// sentence differs.
	StaleCause string
}

// Action is one consequence for the executor to perform.
type Action struct {
	Rule       string // the rule name, which is also the metric label
	Suppressed bool   // guard hit: perform no mint
	NoteTask   string // when suppressed on accept: note the absorbed event here (§5)
	// MintApproval discriminates the one consequence that is not a task
	// mint: materialize the document's awaiting approvals row (029 §7.3).
	// The mint parameters below stay empty on such an action.
	MintApproval bool
	// Mint parameters (Suppressed == false && !MintApproval):
	TaskKind string // "review" | "design"
	Title    string
	Body     string
}

// Evaluate applies the hardcoded rules of 025 §15.4 and §7.3. Rules must never
// emit an event this subscriber consumes (no cascades — a rule, reviewed
// here, not a mechanism; §5).
func Evaluate(in Input) []Action {
	switch in.EventType {
	case eventbus.TypeDocumentSubmitted:
		return evaluateSubmitted(in)
	case eventbus.TypeDocumentAccepted:
		return evaluateAccepted(in)
	case TypeDocPatched:
		return evaluatePatched(in)
	case TypeDocStale:
		return evaluateStale(in)
	default:
		// Vendor dotted types (push, …) and any wl: curie this subscriber
		// does not know about fall through here. doc.patched and doc.stale
		// are the dotted backbone types the rules above act on.
		return nil
	}
}

// evaluateSubmitted fires two rules on one submission: the review task
// somebody works (§15.4), and the approvals row that records the decision the
// document is owed (029 §7.3). They have separate guards, so one being
// suppressed says nothing about the other.
//
// Neither suppression carries a NoteTask: the event log's own (source,
// external_id) dedup usually absorbs a same-version resubmit before either
// guard runs, so there is rarely a second event to note anywhere (§15.4).
func evaluateSubmitted(in Input) []Action {
	review := Action{Rule: ruleReviewOnSubmit, Suppressed: true}
	if in.OpenReviewTask == "" {
		review = Action{
			Rule:     ruleReviewOnSubmit,
			TaskKind: "review",
			Title:    "Review: " + in.DocTitle,
			Body:     reviewBody(in),
		}
	}
	approval := Action{Rule: ruleApprovalOnSubmit, MintApproval: true}
	if in.OpenApprovalBound {
		approval = Action{Rule: ruleApprovalOnSubmit, Suppressed: true}
	}
	return []Action{review, approval}
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
		Title:    PlanningTitle(in.DocTitle),
		Body:     PlanningBody(in.DocIRI, in.Version, in.EventID),
	}}
}

// evaluatePatched is §7.3's re-review: a substantive in-place amendment left
// approved text modified since, so the approvers owe a decision on it. A
// non-substantive patch has nothing to re-review — §8.5's note is the whole
// record of it — so the rule does not apply and, like a plan's acceptance,
// counts as no action rather than as a suppression.
func evaluatePatched(in Input) []Action {
	if in.Classification != "substantive" {
		return nil
	}
	if in.OpenReviewTask != "" {
		// The open review task already asks for a decision on this
		// document; a second patch under it is more of the same work.
		return []Action{{Rule: ruleReviewOnPatch, Suppressed: true}}
	}
	return []Action{{
		Rule:     ruleReviewOnPatch,
		TaskKind: "review",
		Title:    "Re-review patched sections of " + in.DocRef + ": " + strings.Join(in.ChangedAnchors, ", "),
		Body:     patchBody(in),
	}}
}

func reviewBody(in Input) string {
	return fmt.Sprintf(`Review %s (version %d).

prov:wasInformedBy wlid:event/%d

Closing this task is the review outcome. Accepting the document is a
separate, deliberate act — %s — which this task does not perform.`,
		in.DocIRI, in.Version, in.EventID, "`lode doc accept`")
}

// PlanningTitle is the title of the planning task 025 §15.4 mints when a
// spec is accepted. It is exported because the Progress page mints the same
// task from a button (066 §3.4): one open planning task per spec, whichever
// act asked for it, so the title has to come from one place.
func PlanningTitle(docTitle string) string {
	return "Plan: decompose " + docTitle + " into plans"
}

// PlanningBody is that task's body. eventID is the event the mint is
// informed by — the acceptance for the rule above, the mint's own
// task.created event for the Progress page's button, which has no earlier
// event to point at.
func PlanningBody(docIRI string, version int, eventID int64) string {
	return fmt.Sprintf(`%s (version %d) was accepted.

Decide how to decompose this spec into plans, and write them.

prov:wasInformedBy wlid:event/%d

Claim this task (%s) before writing anything, so this session's
tokens bill to it instead of going unattributed (025 §15.6).`,
		docIRI, version, eventID, "`lode task claim <this task's id>`")
}

func patchBody(in Input) string {
	sections := "no anchored section"
	if len(in.ChangedAnchors) > 0 {
		sections = strings.Join(in.ChangedAnchors, ", ")
	}
	return fmt.Sprintf(`%s (version %d) was amended in place: %s.

The document stays accepted — only the patched sections are approved text
that has changed since (025 §7.3). Review them, and decide each approval
lane the patch reopened.

prov:wasInformedBy wlid:event/%d

The marks clear when the last reopened lane is approved; nothing here
accepts the document again.`,
		in.DocIRI, in.Version, sections, in.EventID)
}
