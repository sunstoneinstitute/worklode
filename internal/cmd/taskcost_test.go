package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestTaskCostCmd(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Cost me")
	ctx := context.Background()

	if _, _, err := c.ClaimTask(ctx, task.ID, "host:/wt-1", 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, err := c.TouchAgentSession(ctx, task.ID, "claude-code", "", "sess-1", nil); err != nil {
		t.Fatalf("touch agent session: %v", err)
	}
	usage := []model.SessionUsageBucket{{
		Day: "2026-07-31", Model: "claude-sonnet-5", InputTokens: 1_000_000, OutputTokens: 100_000,
	}}
	if err := c.EndAgentSession(ctx, task.ID, model.EndAgentSessionInput{
		Agent: "claude-code", SessionID: "sess-1", Usage: usage,
	}); err != nil {
		t.Fatalf("end agent session: %v", err)
	}

	// --json is the raw server response.
	out, err := runLode(t, "task", "cost", task.ID, "--json")
	if err != nil {
		t.Fatalf("lode task cost --json: %v\noutput: %s", out, err)
	}
	var tc model.TaskCost
	if err := json.Unmarshal([]byte(out), &tc); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if tc.Task != task.ID || tc.IncludesChildren || tc.Sessions != 1 {
		t.Fatalf("task cost = %+v, want task %s, includes_children=false, sessions=1", tc, task.ID)
	}
	if len(tc.Cost.Totals) != 1 || tc.Cost.Totals[0].CostAmount != "3.000000" {
		t.Fatalf("task cost totals = %+v, want one total of 3.000000", tc.Cost.Totals)
	}

	// Human rendering names the task, the session count, and the cost block.
	// --days defaults to 0 (all history), unlike `lode project show`.
	out, err = runLode(t, "task", "cost", task.ID)
	if err != nil {
		t.Fatalf("lode task cost: %v\noutput: %s", err, out)
	}
	want := task.ID + "\n" +
		"sessions with recorded usage: 1\n" +
		"\ncost, all time: 3.00 USD\n" +
		"  2026-07-31  3.00  in 1.0M  cache-w 0  cache-r 0  out 100.0k\n"
	if out != want {
		t.Fatalf("task cost output:\n%s\nwant:\n%s", out, want)
	}

	// --children reaches the client: with no child tasks the report is
	// unchanged, but the header now says so.
	out, err = runLode(t, "task", "cost", task.ID, "--children")
	if err != nil {
		t.Fatalf("lode task cost --children: %v\noutput: %s", err, out)
	}
	if !strings.HasPrefix(out, task.ID+" (including child tasks)\n") {
		t.Fatalf("task cost --children output = %q, want it to name the scope", out)
	}

	// --days clips the window past the usage day: no error, "none recorded".
	out, err = runLode(t, "task", "cost", task.ID, "--days", "1")
	if err != nil {
		t.Fatalf("lode task cost --days 1: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "none recorded") {
		t.Fatalf("task cost --days 1 output = %q, want it to report no cost recorded", out)
	}

	// An unknown task id is a clean error, not a panic or an empty report.
	if out, err := runLode(t, "task", "cost", "nosuch-999"); err == nil {
		t.Fatalf("task cost unknown id: want error, got none; output: %s", out)
	}
}
