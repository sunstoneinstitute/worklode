package ui

import (
	"context"
	"strings"
	"testing"
)

// TestApprovalsQueueMixesKinds renders one queue holding a PR row and a doc
// row. The doc row is the regression: it has no task, and the row used to
// emit <a href="/tasks/"> unconditionally.
func TestApprovalsQueueMixesKinds(t *testing.T) {
	var b strings.Builder
	err := Approvals(ApprovalsView{
		Page: PageProps{Title: "Reviews", ActiveGlobal: "reviews"},
		Rows: []ApprovalRow{{
			ID: 12, Kind: "PR", EntityID: "sunstoneinstitute/worklode#242",
			Title: "Approvals for documents", URL: "https://github.com/x/y/pull/242",
			TaskID: "WL-355", ProjectID: "worklode", ProjectName: "Worklode backbone",
			Age: "3h ago",
		}, {
			ID: 13, Kind: "Document", EntityID: "doc:44",
			Title: "Documents and deliverables", URL: "/docs/44", Revision: "7",
			ProjectID: "worklode", ProjectName: "Worklode backbone",
			Age: "2d ago",
		}},
	}).Render(context.Background(), &b)
	if err != nil {
		t.Fatalf("render Approvals: %v", err)
	}
	body := b.String()
	if strings.Contains(body, `href="/tasks/"`) {
		t.Fatalf("doc row emitted an empty task link:\n%s", body)
	}
	for _, want := range []string{
		`href="https://github.com/x/y/pull/242"`, `href="/tasks/WL-355"`, // the PR row
		`href="/docs/44"`, "version 7", // the doc row
		">PR<", ">Document<", // each row names its kind
		"/approvals/12/decide", "/approvals/13/decide", // both decide forms
	} {
		if !strings.Contains(body, want) {
			t.Errorf("queue missing %q:\n%s", want, body)
		}
	}
}
