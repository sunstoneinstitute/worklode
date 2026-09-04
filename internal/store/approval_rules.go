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
