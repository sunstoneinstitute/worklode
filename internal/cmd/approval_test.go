package cmd

import (
	"io"
	"strings"
	"testing"
)

// TestApprovalRequestNeedsAReviewer: the flag is optional to cobra, so the
// command has to refuse an empty set itself rather than send a request that
// leaves the document waiting on nobody.
func TestApprovalRequestNeedsAReviewer(t *testing.T) {
	cmd := newApprovalRequestCmd()
	cmd.SetArgs([]string{"WL-SPEC-29"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--reviewer") {
		t.Fatalf("err = %v; want it to ask for --reviewer", err)
	}
}

// TestApprovalReviewerFlagIsRepeatable: --reviewer is a StringArray, matching
// `lode task add --skill`. A comma-separated value is one reviewer whose id
// contains a comma, not two reviewers — which is what a StringSlice would
// have silently made of it.
func TestApprovalReviewerFlagIsRepeatable(t *testing.T) {
	cmd := newApprovalRequestCmd()
	if err := cmd.Flags().Parse([]string{"--reviewer", "bob", "--reviewer", "carol"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := cmd.Flags().GetStringArray("reviewer")
	if err != nil {
		t.Fatalf("GetStringArray: %v", err)
	}
	if len(got) != 2 || got[0] != "bob" || got[1] != "carol" {
		t.Fatalf("reviewers = %v, want [bob carol]", got)
	}
}

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
