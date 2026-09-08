package githubauth

import (
	"context"
	"testing"
)

// A branch whose rules include merge_queue reports true; one whose rules are
// something else, or empty, reports false. Same endpoint, same token path —
// only the payload differs.
func TestBranchRules(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		rules []string
		want  bool
	}{
		{"queue", []string{"pull_request", "merge_queue"}, true},
		{"no queue", []string{"pull_request", "required_status_checks"}, false},
		{"no rules at all", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &appFixture{branchRules: map[string][]string{"main": tc.rules}}
			auth := f.start(t)

			got, err := auth.BranchRules(context.Background(), "acme/app", "main")
			if err != nil {
				t.Fatalf("BranchRules: %v", err)
			}
			if got != tc.want {
				t.Errorf("BranchRules = %v, want %v", got, tc.want)
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

	if _, err := auth.BranchRules(context.Background(), "acme/other", "main"); err == nil {
		t.Fatal("BranchRules on an uninstalled repo returned no error")
	}
}
