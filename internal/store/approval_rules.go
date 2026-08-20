package store

import "strings"

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
