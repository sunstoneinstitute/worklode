package store_test

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestDecisionState(t *testing.T) {
	cases := []struct {
		decision, state string
		ok              bool
	}{
		{"approve", "approved", true},
		{"request_changes", "changes_requested", true},
		{"reject", "rejected", true},
		{"approved", "", false}, // states are not decisions
		{"", "", false},
	}
	for _, c := range cases {
		state, ok := store.DecisionState(c.decision)
		if state != c.state || ok != c.ok {
			t.Errorf("DecisionState(%q) = %q,%v; want %q,%v",
				c.decision, state, ok, c.state, c.ok)
		}
	}
}

func TestQualifiedForRole(t *testing.T) {
	role := func(s string) *string { return &s }

	cases := []struct {
		name     string
		required *string
		groups   []string
		want     bool
	}{
		{"nil requirement qualifies everyone", nil, nil, true},
		{"empty requirement qualifies everyone", role(""), nil, true},
		{"member of required group", role("reviewers"), []string{"reviewers"}, true},
		{"non-member of required group", role("reviewers"), []string{"other"}, false},
		{"empty groups do not qualify", role("reviewers"), []string{}, false},
	}
	for _, c := range cases {
		if got := store.QualifiedForRole(c.required, c.groups); got != c.want {
			t.Errorf("%s: QualifiedForRole(%v, %v) = %v, want %v",
				c.name, c.required, c.groups, got, c.want)
		}
	}
}

func TestIsSelfApproval(t *testing.T) {
	cases := []struct {
		name            string
		author, decider string
		want            bool
	}{
		{"equal logins", "octocat", "octocat", true},
		{"case-differing equal logins", "OctoCat", "octocat", true},
		{"different logins", "octocat", "hubot", false},
		{"empty author", "", "octocat", false},
		{"empty decider", "octocat", "", false},
		{"both empty", "", "", false},
	}
	for _, c := range cases {
		if got := store.IsSelfApproval(c.author, c.decider); got != c.want {
			t.Errorf("%s: IsSelfApproval(%q, %q) = %v, want %v",
				c.name, c.author, c.decider, got, c.want)
		}
	}
}
