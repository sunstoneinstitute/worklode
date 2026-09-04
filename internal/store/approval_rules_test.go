package store_test

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestDecisionState(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestMatchFlowOnLabels(t *testing.T) {
	story := model.ApprovalFlow{Name: "story", Rev: "1",
		Match: map[string]string{"kind": "sunstone-story"}}
	byName := model.ApprovalFlow{Name: "custom", Rev: "1"} // empty match
	cases := []struct {
		labels map[string]string
		want   string // "" = no match
	}{
		{map[string]string{"kind": "sunstone-story"}, "story"},
		{map[string]string{"kind": "sunstone-story", "horizon": "bounded"}, "story"},
		{map[string]string{"kind": "engineering"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		got := store.MatchFlow([]model.ApprovalFlow{story, byName}, c.labels)
		name := ""
		if got != nil {
			name = got.Name
		}
		if name != c.want {
			t.Errorf("MatchFlow(%v) = %q, want %q", c.labels, name, c.want)
		}
	}
}

func TestMatchFlowSpecificity(t *testing.T) {
	t.Parallel()
	broad := model.ApprovalFlow{Name: "broad", Rev: "1",
		Match: map[string]string{"kind": "sunstone-story"}}
	narrow := model.ApprovalFlow{Name: "narrow", Rev: "1",
		Match: map[string]string{"kind": "sunstone-story", "horizon": "bounded"}}
	labels := map[string]string{"kind": "sunstone-story", "horizon": "bounded", "extra": "x"}

	// The two-pair match beats the one-pair match, regardless of slice order.
	got := store.MatchFlow([]model.ApprovalFlow{broad, narrow}, labels)
	if got == nil || got.Name != "narrow" {
		t.Errorf("MatchFlow(broad, narrow) = %v, want narrow", got)
	}
	got = store.MatchFlow([]model.ApprovalFlow{narrow, broad}, labels)
	if got == nil || got.Name != "narrow" {
		t.Errorf("MatchFlow(narrow, broad) = %v, want narrow", got)
	}
}

func TestMatchFlowTiesBreakOnName(t *testing.T) {
	t.Parallel()
	zebra := model.ApprovalFlow{Name: "zebra", Rev: "1",
		Match: map[string]string{"kind": "sunstone-story"}}
	apple := model.ApprovalFlow{Name: "apple", Rev: "1",
		Match: map[string]string{"kind": "sunstone-story"}}
	labels := map[string]string{"kind": "sunstone-story"}

	// Equal specificity, slice order reversed either way: name order wins.
	got := store.MatchFlow([]model.ApprovalFlow{zebra, apple}, labels)
	if got == nil || got.Name != "apple" {
		t.Errorf("MatchFlow(zebra, apple) = %v, want apple", got)
	}
	got = store.MatchFlow([]model.ApprovalFlow{apple, zebra}, labels)
	if got == nil || got.Name != "apple" {
		t.Errorf("MatchFlow(apple, zebra) = %v, want apple", got)
	}
}

func TestRequirementsForEntityAgainstShippedStoryFlow(t *testing.T) {
	t.Parallel()
	flows, err := api.LoadApprovalFlows("")
	if err != nil {
		t.Fatal(err)
	}
	var story model.ApprovalFlow
	for _, f := range flows {
		if f.Name == "story" {
			story = f
		}
	}
	if story.Name == "" {
		t.Fatal("shipped flows do not include story")
	}

	cases := []struct {
		entityKind, name string
		wantLanes        []string
	}{
		{"deliverable", "Methodology", []string{"methodology/science-lead", "methodology/domain-expert"}},
		{"deliverable", "Scientific report", []string{"report/buddy", "report/expert", "report/journalist"}},
		{"deliverable", "Reproducible analysis", []string{"analysis/peer"}},
		{"deliverable", "Interview notes", nil},
		{"task", "anything", nil},
	}
	for _, c := range cases {
		got := store.RequirementsForEntity(story, c.entityKind, c.name)
		gotLanes := make([]string, len(got))
		for i, r := range got {
			gotLanes[i] = r.Lane
		}
		if !equalUnordered(gotLanes, c.wantLanes) {
			t.Errorf("RequirementsForEntity(story, %q, %q) lanes = %v, want %v",
				c.entityKind, c.name, gotLanes, c.wantLanes)
		}
	}

	// Case-insensitive target match.
	got := store.RequirementsForEntity(story, "deliverable", "METHODOLOGY")
	if len(got) != 2 {
		t.Errorf("case-insensitive target match: got %d lanes, want 2", len(got))
	}
}

func equalUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestValidateFlowRefusals(t *testing.T) {
	t.Parallel()
	valid := func() model.ApprovalFlow {
		return model.ApprovalFlow{
			Name: "x", Rev: "1",
			Requirements: []model.ApprovalRequirement{
				{Lane: "a", EntityKind: "task", Role: "r"},
			},
		}
	}
	cases := []struct {
		name    string
		mutate  func(model.ApprovalFlow) model.ApprovalFlow
		wantErr bool
	}{
		{"valid flow", func(f model.ApprovalFlow) model.ApprovalFlow { return f }, false},
		{"no name", func(f model.ApprovalFlow) model.ApprovalFlow { f.Name = ""; return f }, true},
		{"no rev", func(f model.ApprovalFlow) model.ApprovalFlow { f.Rev = ""; return f }, true},
		{"no lane", func(f model.ApprovalFlow) model.ApprovalFlow {
			f.Requirements[0].Lane = ""
			return f
		}, true},
		{"duplicate lane", func(f model.ApprovalFlow) model.ApprovalFlow {
			f.Requirements = append(f.Requirements, model.ApprovalRequirement{
				Lane: "a", EntityKind: "task", Role: "r2",
			})
			return f
		}, true},
		{"unknown entity kind", func(f model.ApprovalFlow) model.ApprovalFlow {
			f.Requirements[0].EntityKind = "milestone"
			return f
		}, true},
		{"pr entity kind refused", func(f model.ApprovalFlow) model.ApprovalFlow {
			f.Requirements[0].EntityKind = "pr"
			return f
		}, true},
		{"no role", func(f model.ApprovalFlow) model.ApprovalFlow {
			f.Requirements[0].Role = ""
			return f
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := store.ValidateFlow(c.mutate(valid()))
			if (err != nil) != c.wantErr {
				t.Errorf("ValidateFlow() error = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}
