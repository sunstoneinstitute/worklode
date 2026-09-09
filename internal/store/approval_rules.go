package store

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// DecisionState maps a submitted decision to the approval state it records.
// ok is false for anything but the three defined decisions.
func DecisionState(decision string) (state string, ok bool) {
	switch decision {
	case "approve":
		return "approved", true
	case "request_changes":
		return "changes_requested", true
	case "reject":
		return "rejected", true
	default:
		return "", false
	}
}

// QualifiedForRole reports whether an actor holding groups may resolve an
// approval requiring requiredRole. A nil or empty requirement qualifies
// everyone.
func QualifiedForRole(requiredRole *string, groups []string) bool {
	if requiredRole == nil || *requiredRole == "" {
		return true
	}
	for _, g := range groups {
		if g == *requiredRole {
			return true
		}
	}
	return false
}

// IsSelfApproval reports whether authorLogin and deciderLogin name the same
// GitHub account. GitHub logins are case-insensitive; either side being
// unknown ("") is not self-approval — the check refuses only what it can
// prove (029 §7.1's default refusal, not a guess).
func IsSelfApproval(authorLogin, deciderLogin string) bool {
	if authorLogin == "" || deciderLogin == "" {
		return false
	}
	return strings.EqualFold(authorLogin, deciderLogin)
}

// FlowEntityKinds are the entity kinds a review flow may demand a decision on
// (029 §7.2): a subset of model.ApprovalEntityKinds, same spelling. 'pr' is
// absent on purpose: PR approval rows come from the GitHub ingest, not from
// a flow.
var FlowEntityKinds = []string{"doc", "deliverable", "task"}

// ValidateFlow reports what makes a flow unusable. Pure, so the loader can
// refuse a bad configuration file at boot and the rule engine can reuse it.
func ValidateFlow(f model.ApprovalFlow) error {
	if f.Name == "" {
		return errors.New("flow has no name")
	}
	if f.Rev == "" {
		return fmt.Errorf("flow %q has no rev", f.Name)
	}
	seen := make(map[string]bool, len(f.Requirements))
	for _, r := range f.Requirements {
		switch {
		case r.Lane == "":
			return fmt.Errorf("flow %q: a requirement has no lane", f.Name)
		case seen[r.Lane]:
			return fmt.Errorf("flow %q: lane %q is declared twice", f.Name, r.Lane)
		case !slices.Contains(FlowEntityKinds, r.EntityKind):
			return fmt.Errorf("flow %q lane %q: entity_kind %q is not one of %v",
				f.Name, r.Lane, r.EntityKind, FlowEntityKinds)
		case r.Role == "":
			return fmt.Errorf("flow %q lane %q: no role", f.Name, r.Lane)
		}
		seen[r.Lane] = true
	}
	return nil
}

// MatchFlow picks the flow that governs a project with these labels: every
// pair in a flow's match must be present in labels. Among matches the most
// specific (largest match set) wins; ties break on name. A flow with an
// empty match never auto-matches — it is only ever applied by name. Nil
// when nothing matches.
func MatchFlow(flows []model.ApprovalFlow, labels map[string]string) *model.ApprovalFlow {
	var best *model.ApprovalFlow
	for i, f := range flows {
		if len(f.Match) == 0 || !matchesLabels(f.Match, labels) {
			continue
		}
		if best == nil || len(f.Match) > len(best.Match) ||
			(len(f.Match) == len(best.Match) && f.Name < best.Name) {
			best = &flows[i]
		}
	}
	return best
}

// matchesLabels reports whether every pair in match is present in labels.
func matchesLabels(match, labels map[string]string) bool {
	for k, v := range match {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// RevisionOutcome is what designating a new revision does to an entity's
// approval history (029 §7.1).
type RevisionOutcome int

const (
	// RevisionNoop: a row already binds this revision, or nothing was ever
	// required — a push never conjures a requirement.
	RevisionNoop RevisionOutcome = iota
	// RevisionRebind: the open review row moves to the new revision. The
	// requirement was never decided; the decision must bind what the
	// reviewer will see.
	RevisionRebind
	// RevisionCandidate: only decided history exists. A new awaiting row is
	// the visibly unreviewed candidate; the decided rows keep their exact
	// revisions.
	RevisionCandidate
)

// OnNewRevision decides the outcome. open is the entity's open review-kind
// row (nil when none); hasDecided reports whether any decided review-kind
// row exists; boundAlready reports whether any review-kind row (open or
// decided) already carries exactly the new revision.
func OnNewRevision(open *Approval, hasDecided, boundAlready bool) RevisionOutcome {
	switch {
	case boundAlready:
		return RevisionNoop
	case open != nil:
		return RevisionRebind
	case hasDecided:
		return RevisionCandidate
	default:
		return RevisionNoop
	}
}

// PriorApprover reports whether actorID is the resolving actor of an
// 'approved' review-kind row in history (029 §7.1: "a qualified prior
// approver confirms the existing decision still holds or reopens it").
// Impact rows in history prove nothing and are ignored.
func PriorApprover(history []Approval, actorID string) bool {
	for _, a := range history {
		if a.ReviewKind == "impact" {
			continue
		}
		if a.State != "approved" {
			continue
		}
		if a.ResolvingActor != nil && *a.ResolvingActor == actorID {
			return true
		}
	}
	return false
}

// ImpactEffect maps a decision state recorded on an impact row to its side
// effect on the dependent's review row.
type ImpactEffect int

const (
	ImpactConfirm ImpactEffect = iota // approved: the prior decision holds
	ImpactReopen                      // changes_requested | rejected: reopen
)

// ImpactDecisionEffect maps a decision state recorded on an impact row to
// its effect. Anything but 'approved' reopens — a fail-safe default rather
// than treating an unrecognized state as confirmation.
func ImpactDecisionEffect(state string) ImpactEffect {
	if state == "approved" {
		return ImpactConfirm
	}
	return ImpactReopen
}

// SelfReviewExceptionValid reports whether an author may decide their own
// work (029 §7.1): only when the effective review policy allows it AND a
// different authorized actor approved the exception before review.
// authorizedBy == the decider is not "a different actor".
func SelfReviewExceptionValid(policyAllows bool, authorizedBy *string, decider string) bool {
	if !policyAllows {
		return false
	}
	if authorizedBy == nil || *authorizedBy == "" {
		return false
	}
	return *authorizedBy != decider
}

// RequirementsForEntity returns the lanes a flow demands of one entity:
// requirements whose EntityKind matches and whose Target is empty or
// equals name case-insensitively. Deterministic lane order.
func RequirementsForEntity(f model.ApprovalFlow, entityKind, name string) []model.ApprovalRequirement {
	var out []model.ApprovalRequirement
	for _, r := range f.Requirements {
		if r.EntityKind != entityKind {
			continue
		}
		if r.Target == "" || strings.EqualFold(r.Target, name) {
			out = append(out, r)
		}
	}
	return out
}
