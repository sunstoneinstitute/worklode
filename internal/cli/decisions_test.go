package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestDecisionTable(t *testing.T) {
	var b strings.Builder
	cli.DecisionTable(&b, []model.Decision{
		{Task: "WL-643", Key: "x-distribution", Group: "scope", ResponseType: "single_select",
			Question: "Which channel?"},
		{Task: "WL-643", Key: "ship-date", ResponseType: "freetext", Question: "When?",
			Answer: &model.DecisionAnswer{Freetext: "October"}},
	})
	out := b.String()
	for _, want := range []string{"KEY", "ANSWERED", "x-distribution", "scope", "single_select",
		"Which channel?", "ship-date", "freetext"} {
		if !strings.Contains(out, want) {
			t.Fatalf("DecisionTable output missing %q:\n%s", want, out)
		}
	}
	// The unanswered row has no group and no answer; the answered one has both
	// columns filled the other way.
	unanswered, answered := lineWith(t, out, "x-distribution"), lineWith(t, out, "ship-date")
	if !strings.Contains(unanswered, "no") || !strings.Contains(answered, "yes") {
		t.Fatalf("answered column wrong:\n%s", out)
	}
	if !strings.Contains(answered, "-") {
		t.Fatalf("groupless row should show a dash:\n%s", answered)
	}
}

func TestDecisionRenderUnanswered(t *testing.T) {
	var b strings.Builder
	min, max := 1, 2
	cli.DecisionRender(&b, model.Decision{
		Task: "WL-643", Key: "x-distribution", Position: 2, Group: "scope",
		Question: "Which channels do we ship on?", Context: "Budget is fixed.",
		ResponseType: "multi_select", MinPicks: &min, MaxPicks: &max,
		Options: []model.DecisionOption{
			{Label: "Direct"},
			{Label: "Partner", Description: "through the reseller"},
		},
	})
	out := b.String()
	for _, want := range []string{"WL-643/x-distribution", "Which channels do we ship on?",
		"multi_select", "1-2", "Direct", "Partner", "through the reseller",
		"scope", "Budget is fixed."} {
		if !strings.Contains(out, want) {
			t.Fatalf("DecisionRender output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "decided") {
		t.Fatalf("unanswered row rendered a decision line:\n%s", out)
	}
}

func TestDecisionRenderAnswered(t *testing.T) {
	var b strings.Builder
	at := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	cli.DecisionRender(&b, model.Decision{
		Task: "WL-643", Key: "ship-date", Position: 1, Question: "When do we ship?",
		ResponseType: "freetext",
		Answer:       &model.DecisionAnswer{Freetext: "October"},
		DecidedBy:    "stig", DecidedAt: &at,
	})
	out := b.String()
	if !strings.Contains(out, "decided:") || !strings.Contains(out, "stig") {
		t.Fatalf("DecisionRender output missing the decision line:\n%s", out)
	}
	if !strings.Contains(out, cli.LocalTime(at)) {
		t.Fatalf("DecisionRender did not use LocalTime:\n%s", out)
	}
}

// lineWith returns the single output line containing needle.
func lineWith(t *testing.T, out, needle string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line contains %q:\n%s", needle, out)
	return ""
}
