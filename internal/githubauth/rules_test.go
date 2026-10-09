package githubauth

import (
	"context"
	"testing"
)

// A branch whose rules include merge_queue or pull_request reports true for
// that rule; one whose rules are something else, or empty, reports false. Same endpoint, same token path —
// only the payload differs.
func TestBranchRules(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		rules  []string
		want   bool
		wantPR bool
	}{
		{"queue", []string{"pull_request", "merge_queue"}, true, true},
		{"no queue", []string{"pull_request", "required_status_checks"}, false, true},
		{"no PR rule", []string{"required_status_checks"}, false, false},
		{"no rules at all", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &appFixture{branchRules: map[string][]string{"main": tc.rules}}
			auth := f.start(t)

			got, gotPR, err := auth.BranchRules(context.Background(), "acme/app", "main")
			if err != nil {
				t.Fatalf("BranchRules: %v", err)
			}
			if got != tc.want || gotPR != tc.wantPR {
				t.Errorf("BranchRules = (%v, %v), want (%v, %v)", got, gotPR, tc.want, tc.wantPR)
			}
			if !f.called("GET", "/repos/acme/app/rules/branches/main") {
				t.Error("the rules endpoint was never called")
			}
			f.assertTokenAuth(t, "/repos/acme/app/rules/branches/main")
		})
	}
}

// A repo the App is not installed on leaves the answer unknown: the caller
// must get an error, never a false that would be recorded as "no queue".
func TestBranchRulesAppNotInstalled(t *testing.T) {
	t.Parallel()
	f := &appFixture{}
	auth := f.start(t)

	if _, _, err := auth.BranchRules(context.Background(), "acme/other", "main"); err == nil {
		t.Fatal("BranchRules on an uninstalled repo returned no error")
	}
}
