package mcp_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

const approvalInstruction = "Ask the user with your question tool (e.g. AskUserQuestion), offering these choices and an optional note. In a voice conversation, read the approval and the choices and wait for the verdict. Then call approval_decide with that verdict and confirmed=true."

func TestReviewTools(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	alice := f.connect(t, f.alice, oldProtocol, nil)
	bob := f.connect(t, f.bob, oldProtocol, nil)
	h := handleOf(t, call(t, alice, "project_use", map[string]any{"project": "WL", "confirmed": true}))
	api := f.client(f.alice)

	res := call(t, alice, "doc_add", map[string]any{"project": h, "kind": "spec", "title": "Reviewed thing", "body": "## 1. Scope {#sec-1}\n\nText.\n"})
	if res.IsError {
		t.Fatalf("doc_add = %s", text(res, 0))
	}
	doc, err := api.ResolveDoc(ctx, "WL-SPEC-1")
	if err != nil {
		t.Fatal(err)
	}
	res = call(t, alice, "approval_add", map[string]any{"project": h, "entity": "spec one", "approver": "bob"})
	if res.IsError {
		t.Fatalf("approval_add = %s", text(res, 0))
	}
	id, _ := strconv.ParseInt(strings.Fields(text(res, 0))[1], 10, 64)

	state := func(id int64) string {
		t.Helper()
		r, _, err := api.ListApprovals(ctx, model.ApprovalListParams{})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range r.Approvals {
			if a.ID == id {
				return a.State
			}
		}
		return "decided"
	}
	if res := call(t, bob, "approval_list", nil); res.IsError || !strings.Contains(text(res, 1), "Reviewed thing") {
		t.Fatalf("approval_list = %+v", res.Content)
	}

	// A verdict without confirmed=true decides nothing.
	res = call(t, bob, "approval_decide", map[string]any{"id": id, "verdict": "approve"})
	d := detail(t, res)
	if res.IsError || d["instruction"] != approvalInstruction || d["approver"] != "bob" || d["entity"] != "doc "+model.DocEntityID(doc.ID) || len(d["choices"].([]any)) != 3 {
		t.Fatalf("fallback = %+v", res.Content)
	}
	if s := state(id); s != "awaiting" {
		t.Fatalf("unconfirmed verdict left %q", s)
	}

	// The confirmed verdict decides it.
	if res := call(t, bob, "approval_decide", map[string]any{"id": id, "verdict": "request changes", "note": "tests", "confirmed": true}); res.IsError {
		t.Fatalf("confirmed decide = %s", text(res, 0))
	}
	if s := state(id); s != "decided" {
		t.Fatalf("confirmed verdict left %q", s)
	}

	// doc_reviewers sets and reads; approval_request opens bob's lane.
	if res := call(t, alice, "doc_reviewers", map[string]any{"project": h, "id": "spec 1", "reviewers": []string{"bob"}}); res.IsError {
		t.Fatalf("doc_reviewers set = %s", text(res, 0))
	}
	if res := call(t, alice, "doc_reviewers", map[string]any{"project": h, "id": "spec 1"}); res.IsError || !strings.Contains(text(res, 0), "reviewed by bob") {
		t.Fatalf("doc_reviewers = %+v", res.Content)
	}
	if res := call(t, alice, "approval_request", map[string]any{"project": h, "doc": "spec 1"}); res.IsError {
		t.Fatalf("approval_request = %s", text(res, 0))
	}
	r, _, _ := api.ListApprovals(ctx, model.ApprovalListParams{})
	var docID int64
	for _, a := range r.Approvals {
		if a.Lane != "" {
			docID = a.ID
		}
	}

	// Elicitation only for a capable client; the accepted retry decides.
	var asked []string
	accept := func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
		asked = append(asked, req.Params.Message)
		return &sdk.ElicitResult{Action: "accept", Content: map[string]any{"verdict": "approve", "note": "fine"}}, nil
	}
	for _, c := range []*sdk.ClientSession{f.connect(t, f.bob, newProtocol, nil), f.connect(t, f.bob, oldProtocol, accept)} {
		if res := call(t, c, "approval_decide", map[string]any{"id": docID}); res.IsError || detail(t, res)["instruction"] != approvalInstruction {
			t.Fatalf("incapable client = %+v", res.Content)
		}
	}
	if len(asked) != 0 || state(docID) != "awaiting" {
		t.Fatalf("elicited %d times or decided", len(asked))
	}
	if res := call(t, f.connect(t, f.bob, newProtocol, accept), "approval_decide", map[string]any{"id": docID}); res.IsError {
		t.Fatalf("elicited decide = %s", text(res, 0))
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "Reviewed thing") || !strings.Contains(asked[0], "revision 1") || !strings.Contains(asked[0], "bob") {
		t.Fatalf("elicitation = %q", asked)
	}
	if s := state(docID); s != "decided" {
		t.Fatalf("elicited verdict left %q", s)
	}

	// A role-gated row with stale group claims answers with the reconnect text.
	gated, _, err := api.RequireApproval(ctx, model.RequireApprovalInput{EntityKind: "doc", EntityID: model.DocEntityID(doc.ID), Lane: "ops", Role: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	res = call(t, bob, "approval_decide", map[string]any{"id": gated.ID, "verdict": "approve", "confirmed": true})
	if !res.IsError || !strings.Contains(text(res, 0), "Reconnect the Worklode connector") || f.count(t, "approval_decide", "refused") != 1 {
		t.Fatalf("stale groups = %+v", res.Content)
	}
}

// WL-1227: approval_add on a task binds a revision, so the row can be decided.
func TestTaskApprovalDecides(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	alice := f.connect(t, f.alice, oldProtocol, nil)
	bob := f.connect(t, f.bob, oldProtocol, nil)
	h := handleOf(t, call(t, alice, "project_use", map[string]any{"project": "WL", "confirmed": true}))
	api := f.client(f.alice)

	task, _, err := api.CreateTask(ctx, model.CreateTaskInput{Project: "worklode", Title: "Approve me", Kind: "chore", Priority: "low"})
	if err != nil {
		t.Fatal(err)
	}
	res := call(t, alice, "approval_add", map[string]any{"project": h, "entity": task.ID, "approver": "bob"})
	if res.IsError {
		t.Fatalf("approval_add = %s", text(res, 0))
	}
	id, _ := strconv.ParseInt(strings.Fields(text(res, 0))[1], 10, 64)
	if res := call(t, bob, "approval_decide", map[string]any{"id": id, "verdict": "approve", "confirmed": true}); res.IsError {
		t.Fatalf("approval_decide = %s", text(res, 0))
	}
}

// TestApprovalDecideChangesRequested decides a changes_requested row through
// MCP (WL-REQ-1608) and lists approvals by each state.
func TestApprovalDecideChangesRequested(t *testing.T) {
	f := newFixture(t)
	alice := f.connect(t, f.alice, oldProtocol, nil)
	bob := f.connect(t, f.bob, oldProtocol, nil)
	h := handleOf(t, call(t, alice, "project_use", map[string]any{"project": "WL", "confirmed": true}))
	if res := call(t, alice, "doc_add", map[string]any{"project": h, "kind": "spec", "title": "Twice decided", "body": "## 1. Scope {#sec-1}\n\nText.\n"}); res.IsError {
		t.Fatalf("doc_add = %s", text(res, 0))
	}
	doc, err := f.client(f.alice).ResolveDoc(t.Context(), "WL-SPEC-1")
	if err != nil {
		t.Fatal(err)
	}
	// One row per lane: the key is (entity, revision, lane), so a second
	// requirement on the same lane would return the first row.
	add := func(lane string) int64 {
		t.Helper()
		a, _, err := f.client(f.alice).RequireApproval(t.Context(), model.RequireApprovalInput{
			EntityKind: "doc", EntityID: model.DocEntityID(doc.ID), Lane: lane, Actor: "bob"})
		if err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	decide := func(id int64, verdict string) {
		t.Helper()
		if res := call(t, bob, "approval_decide", map[string]any{"id": id, "verdict": verdict, "confirmed": true}); res.IsError {
			t.Fatalf("approval_decide %d %s = %s", id, verdict, text(res, 0))
		}
	}
	listed := func(state string, want int) {
		t.Helper()
		res := call(t, bob, "approval_list", map[string]any{"state": state})
		if res.IsError {
			t.Fatalf("approval_list %s = %s", state, text(res, 0))
		}
		if got := text(res, 0); !strings.HasPrefix(got, strconv.Itoa(want)+" approvals") && !(want == 0 && strings.HasPrefix(got, "No approvals")) {
			t.Fatalf("approval_list %s = %q, want %d", state, got, want)
		}
	}

	changed := add("changed")
	decide(changed, "request changes")
	listed("changes_requested", 1)
	listed("awaiting", 0)

	decide(changed, "approve")
	listed("changes_requested", 0)
	listed("approved", 1)

	rejected := add("rejected")
	decide(rejected, "reject")
	listed("rejected", 1)

	add("waiting")
	listed("awaiting", 1)
	listed("", 1)
}
