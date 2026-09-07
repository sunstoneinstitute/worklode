package ui

import (
	"context"
	"strings"
	"testing"
)

// TestApprovalsQueueMixesKinds renders one queue holding a PR row, a doc row
// and a deliverable lane with no designated revision. The doc row is the
// regression: it has no task, and the row used to emit <a href="/tasks/">
// unconditionally. The deliverable row pins 029 §7.2's two additions: the
// lane is visible, and a row nobody can decide yet offers no decide form.
func TestApprovalsQueueMixesKinds(t *testing.T) {
	var b strings.Builder
	err := Approvals(ApprovalsView{
		Page: PageProps{Title: "Reviews"},
		Rows: []ApprovalRow{{
			ID: 12, Kind: "pr", EntityID: "sunstoneinstitute/worklode#242",
			Title: "Approvals for documents", URL: "https://github.com/x/y/pull/242",
			TaskID: "WL-355", ProjectID: "worklode", ProjectName: "Worklode backbone",
			Age: "3h ago", Decidable: true,
		}, {
			ID: 13, Kind: "doc", EntityID: "doc:44",
			Title: "Documents and deliverables", URL: "/docs/44", Revision: "7",
			Lane: "stig", ProjectID: "worklode", ProjectName: "Worklode backbone",
			Age: "2d ago", Decidable: true,
		}, {
			ID: 14, Kind: "deliverable", EntityID: "WL-DEL-3",
			Title: "Methodology", URL: "/projects/worklode/deliverables",
			Lane: "methodology/science-lead", ProjectID: "worklode",
			ProjectName: "Worklode backbone", Age: "1d ago",
		}},
	}).Render(context.Background(), &b)
	if err != nil {
		t.Fatalf("render Approvals: %v", err)
	}
	body := b.String()
	if strings.Contains(body, `href="/tasks/"`) {
		t.Fatalf("doc row emitted an empty task link:\n%s", body)
	}
	if strings.Contains(body, "/approvals/14/decide") {
		t.Errorf("undesignated row offered a decide form:\n%s", body)
	}
	for _, want := range []string{
		`href="https://github.com/x/y/pull/242"`, `href="/tasks/WL-355"`, // the PR row
		`href="/docs/44"`, "version 7", // the doc row
		">pr<", ">doc<", ">deliverable<", // each row names its kind
		">methodology/science-lead<",                   // and the lane it answers
		"No revision designated for review yet.",       // the undecidable row
		"/approvals/12/decide", "/approvals/13/decide", // both decide forms
	} {
		if !strings.Contains(body, want) {
			t.Errorf("queue missing %q:\n%s", want, body)
		}
	}
}
