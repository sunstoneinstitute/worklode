package mcp_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

const decisionInstruction = "Ask the user with your question tool (e.g. AskUserQuestion), offering these options. In a voice conversation, read the question and options and wait for the answer. Then call decision_resolve with that answer and confirmed=true."

// assertAsk checks the fallback answer: the question, the options and the
// WL-REQ-1843 instruction, verbatim.
func assertAsk(t *testing.T, res *sdk.CallToolResult) {
	t.Helper()
	if res.IsError {
		t.Fatalf("want the fallback answer, got error %+v", res.Content)
	}
	d := detail(t, res)
	opts, _ := d["options"].([]any)
	if d["question"] != "Which database?" || len(opts) != 2 || opts[0] != "Postgres" || d["instruction"] != decisionInstruction {
		t.Fatalf("fallback = %v", d)
	}
}

func TestDecisionTools(t *testing.T) {
	f := newFixture(t)
	cs := f.connect(t, f.alice, oldProtocol, nil)
	h := handleOf(t, call(t, cs, "project_use", map[string]any{"project": "WL", "confirmed": true}))
	api := f.client(f.alice)
	ctx := t.Context()
	// Recording an answer assigns the task, so the decider is on the crew.
	if _, _, err := api.AddCrewMember(ctx, "worklode", "alice", "", false, false); err != nil {
		t.Fatal(err)
	}
	task, _, err := api.CreateTask(ctx, model.CreateTaskInput{Project: "worklode", Title: "Pick a store", Kind: "chore", Priority: "low"})
	if err != nil {
		t.Fatal(err)
	}
	spoken := "task " + strings.TrimPrefix(task.ID, "WL-")

	add := func(q string, opts ...string) string {
		t.Helper()
		res := call(t, cs, "decision_add", map[string]any{"project": h, "task": spoken, "question": q, "options": opts})
		if res.IsError {
			t.Fatalf("decision_add = %+v", res.Content)
		}
		return strings.TrimSuffix(strings.TrimPrefix(strings.Fields(text(res, 0))[1], task.ID+"/"), ":")
	}
	key := add("Which database?", "Postgres", "SQLite")
	if key != "which-database" {
		t.Fatalf("key = %q", key)
	}

	if res := call(t, cs, "decision_list", map[string]any{"project": h, "task": spoken}); res.IsError || !strings.Contains(text(res, 1), "Which database?") {
		t.Fatalf("decision_list = %+v", res.Content)
	}
	if res := call(t, cs, "decision_show", map[string]any{"task": task.ID, "key": key}); res.IsError || !strings.Contains(text(res, 1), "SQLite") {
		t.Fatalf("decision_show = %+v", res.Content)
	}

	unanswered := func(k string) bool {
		d, _, err := api.GetDecision(ctx, task.ID, k)
		if err != nil {
			t.Fatal(err)
		}
		return d.Answer == nil
	}

	// An answer without confirmed=true records nothing.
	assertAsk(t, call(t, cs, "decision_resolve", map[string]any{"project": h, "task": spoken, "key": key, "answer": "Postgres"}))
	if !unanswered(key) {
		t.Fatal("answer without confirmed was recorded")
	}

	// The confirmed answer records the pick.
	if res := call(t, cs, "decision_resolve", map[string]any{"project": h, "task": spoken, "key": key, "answer": "postgres", "confirmed": true}); res.IsError {
		t.Fatalf("confirmed resolve = %s", text(res, 0))
	}
	d, _, _ := api.GetDecision(ctx, task.ID, key)
	if d.Answer == nil || !slices.Equal(d.Answer.Picked, []string{"Postgres"}) || d.DecidedBy != "alice" {
		t.Fatalf("answer = %+v by %q", d.Answer, d.DecidedBy)
	}

	// Elicitation only for a capable client; the accepted retry records.
	var asked []string
	accept := func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
		asked = append(asked, req.Params.Message)
		return &sdk.ElicitResult{Action: "accept", Content: map[string]any{"answer": "Neither, use files"}}, nil
	}
	k2 := add("Which database for tests?", "Postgres", "SQLite")
	for _, c := range []*sdk.ClientSession{f.connect(t, f.alice, newProtocol, nil), f.connect(t, f.alice, oldProtocol, accept)} {
		res := call(t, c, "decision_resolve", map[string]any{"task": task.ID, "key": k2})
		if res.IsError || detail(t, res)["instruction"] != decisionInstruction {
			t.Fatalf("incapable client = %+v", res.Content)
		}
	}
	if len(asked) != 0 || !unanswered(k2) {
		t.Fatalf("elicited %d times or recorded", len(asked))
	}
	if res := call(t, f.connect(t, f.alice, newProtocol, accept), "decision_resolve", map[string]any{"task": task.ID, "key": k2}); res.IsError {
		t.Fatalf("elicited resolve = %+v", res.Content)
	}
	if len(asked) != 1 || asked[0] != "Which database for tests?" {
		t.Fatalf("elicitation = %q", asked)
	}
	d, _, _ = api.GetDecision(ctx, task.ID, k2)
	if d.Answer == nil || d.Answer.Freetext != "Neither, use files" {
		t.Fatalf("elicited answer = %+v", d.Answer)
	}
}
