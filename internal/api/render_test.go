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
