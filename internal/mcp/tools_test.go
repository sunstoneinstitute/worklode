package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// wantTools is the WL-REQ-1824 tool table, transcribed. A tool added or
// dropped without changing that rule fails TestToolsListMatchesTable.
var wantTools = []string{
	"project_list", "project_resolve", "project_use",
	"search", "show",
	"file",
	"task_list", "task_add", "task_update", "task_set_state",
	"doc_list", "doc_add", "doc_edit", "doc_transition",
	"rule_list", "rule_add", "rule_edit", "rule_link", "rule_supersede",
	"doc_reviewers", "approval_request", "approval_list", "approval_add", "approval_decide",
	"decision_list", "decision_show", "decision_add", "decision_resolve",
	"lode", // WL-REQ-1862
}

func TestToolsListMatchesTable(t *testing.T) {
	rr := post(t, New(Config{API: stubAPI}), "wl_test", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	body := rr.Body.String()
	if i := strings.Index(body, "data: "); i >= 0 {
		body = body[i+len("data: "):]
	}
	var resp struct {
		Result struct {
			Tools []struct{ Name string }
		}
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &resp); err != nil {
		t.Fatalf("tools/list = %d %s: %v", rr.Code, rr.Body, err)
	}
	var got []string
	for _, tl := range resp.Result.Tools {
		got = append(got, tl.Name)
	}
	want := slices.Clone(wantTools)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("tools/list = %v\nwant (WL-REQ-1824) %v", got, want)
	}
}
