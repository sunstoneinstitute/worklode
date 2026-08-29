package cmd

import (
	"testing"
)

// TestApprovalHasNoDecisionCommand is the CLI half of 029 §7.3: approving is
// a web UI act because an OIDC session's group claims are fresh and a 30-day
// CLI token's are not. `lode approval` therefore offers request and list and
// nothing else, and the absence is asserted so a later "for convenience"
// addition trips a test rather than a spec review.
func TestApprovalHasNoDecisionCommand(t *testing.T) {
	want := map[string]bool{"request": true, "list": true}
	for _, sub := range newApprovalCmd().Commands() {
		if !want[sub.Name()] {
			t.Errorf("lode approval %s exists; 029 §7.3 keeps every decision on the web session",
				sub.Name())
		}
		delete(want, sub.Name())
	}
	for name := range want {
		t.Errorf("lode approval %s is missing", name)
	}
}
