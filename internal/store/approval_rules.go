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
// (029 §7.2). 'pr' is absent on purpose: PR approval rows come from the
// GitHub ingest, not from a flow.
var FlowEntityKinds = []string{"document", "deliverable", "task"}

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
