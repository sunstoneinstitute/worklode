package api

import (
	"context"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/mdrender"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// The cockpit's automation-boundary card shows overhead's share of a
// project's spend, so the mapping has to carry it across (spec 052 §4).
func TestCockpitCostTotalsIncludesOverhead(t *testing.T) {
	t.Parallel()
	report := model.CostReport{Totals: []model.CostTotals{{
		Currency:   "USD",
		CostAmount: "1.500000",
		Overhead:   model.CostOverhead{CostAmount: "1.300000"},
	}}}
	got := cockpitCostTotals(report)
	if len(got) != 1 || got[0].OverheadCostAmount != "1.300000" {
		t.Fatalf("cockpitCostTotals = %+v, want OverheadCostAmount 1.300000", got)
	}
}

// TestTaskPageRendersCallout is the full-page-path regression for WL-417's
// callout styling: a task body with a GitHub alert renders, end to end
// through mdrender -> taskView -> ui.Task, as the pinned
// <aside class="callout callout-KIND"><p class="callout-title"> markup
// inside .prose, not a plain blockquote. taskView's md may be nil (renders
// afresh, see its doc comment), so this needs neither a store nor an HTTP
// server.
func TestTaskPageRendersCallout(t *testing.T) {
	t.Parallel()
	task := &model.Task{ID: "WL-1", Project: "proj", Title: "t", Body: "> [!WARNING]\n> Read this first."}
	view := taskView(nil, mdrender.ProjectKeys{}, task, ui.CockpitProject{}, false, nil, nil, nil)

	var b strings.Builder
	if err := ui.Task(view).Render(context.Background(), &b); err != nil {
		t.Fatalf("render Task: %v", err)
	}
	body := b.String()
	for _, want := range []string{
		`<aside class="callout callout-warning">`,
		`<p class="callout-title">Warning</p>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered task page missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<blockquote>") {
		t.Fatalf("callout rendered as a plain blockquote too:\n%s", body)
	}
}

// TestTaskPageRendersDetailsAndMark is the same full-page-path regression as
// TestTaskPageRendersCallout, for WL-433's .prose details/summary styling: a
// task body with raw <details><summary> and <mark> HTML must still reach the
// rendered page after mdrender's sanitiser, pinning the allowlist and the
// stylesheet's subjects (.prose details, .prose summary, .prose mark)
// together so a future allowlist change that would orphan those rules fails
// this test instead of shipping silently.
func TestTaskPageRendersDetailsAndMark(t *testing.T) {
	t.Parallel()
	task := &model.Task{ID: "WL-1", Project: "proj", Title: "t", Body: "<details><summary>More</summary>hidden</details>\n\n<mark>hot</mark>"}
	view := taskView(nil, mdrender.ProjectKeys{}, task, ui.CockpitProject{}, false, nil, nil, nil)

	var b strings.Builder
	if err := ui.Task(view).Render(context.Background(), &b); err != nil {
		t.Fatalf("render Task: %v", err)
	}
	body := b.String()
	for _, want := range []string{
		`<details><summary>More</summary>hidden</details>`,
		`<mark>hot</mark>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered task page missing %q:\n%s", want, body)
		}
	}
}

// TestActivitySummary is the derivation table the Activity card reads (spec
// 071 §4): one line per event kind, built from the allowlisted attributes
// alone. The event name is the row's own cell, so no summary repeats it —
// except for a kind this table does not model, where naming the event is the
// honest answer.
func TestActivitySummary(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	cases := []struct {
		name string
		row  model.TaskActivity
		want string
	}{
		{"tool result ok", model.TaskActivity{
			Event: "claude_code.tool_result",
			Attrs: model.ActivityAttrs{ToolName: "Bash", Success: &yes, DurationMS: 1234},
		}, "Bash ok 1.2s"},
		{"tool result failed names the error type", model.TaskActivity{
			Event: "claude_code.tool_result",
			Attrs: model.ActivityAttrs{ToolName: "Edit", Success: &no, DurationMS: 340, ErrorType: "string_not_found"},
		}, "Edit failed 340ms string_not_found"},
		{"tool result with no success recorded omits it", model.TaskActivity{
			Event: "claude_code.tool_result",
			Attrs: model.ActivityAttrs{ToolName: "Read"},
		}, "Read"},
		{"tool decision", model.TaskActivity{
			Event: "claude_code.tool_decision",
			Attrs: model.ActivityAttrs{ToolName: "Bash", DecisionType: "accept", DecisionSource: "config"},
		}, "Bash accept config"},
		{"api request", model.TaskActivity{
			Event: "claude_code.api_request",
			Attrs: model.ActivityAttrs{Model: "claude-opus-4", Attempt: 2},
		}, "claude-opus-4 attempt 2"},
		{"api request on the first attempt says nothing about it", model.TaskActivity{
			Event: "claude_code.api_request",
			Attrs: model.ActivityAttrs{Model: "claude-opus-4", Attempt: 1},
		}, "claude-opus-4"},
		{"api error", model.TaskActivity{
			Event: "claude_code.api_error",
			Attrs: model.ActivityAttrs{StatusCode: 529, Error: "overloaded", Attempt: 3},
		}, "529 overloaded attempt 3"},
		{"user prompt", model.TaskActivity{
			Event: "claude_code.user_prompt",
			Attrs: model.ActivityAttrs{PromptLength: 412, CommandName: "lode:work"},
		}, "412 chars lode:work"},
		{"assistant response", model.TaskActivity{
			Event: "claude_code.assistant_response",
			Attrs: model.ActivityAttrs{Model: "claude-opus-4", ResponseLength: 2048},
		}, "claude-opus-4 2048 chars"},
		{"an event this table does not model names itself", model.TaskActivity{
			Event: "claude_code.something_new",
		}, "claude_code.something_new"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := activitySummary(c.row); got != c.want {
				t.Fatalf("activitySummary = %q, want %q", got, c.want)
			}
		})
	}
}
