//go:build e2e

// decisions_test.go proves 025 §10.1's decision task end to end over public
// HTTP surfaces only: a decision-kind task poses its questions at creation,
// reads them back unanswered and in authored order, stays out of the pickup
// loop and refuses a claim, and closes itself when its last row is answered.
// Nothing here writes to the store directly.
package e2e

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestDecisionTaskJourney(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "dec", Name: "Decide", Key: "DEC",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "dana", Kind: "human", DisplayName: "Dana",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	// Being assigned work is Crew-gated (029 §6.1), so dana joins before the
	// assignment below.
	if _, _, err := admin.AddCrewMember(ctx, "dec", "dana", "member", false, false); err != nil {
		t.Fatalf("add dana to the crew: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "dana", "e2e decisions", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	dana := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	// 1. The task and its two questions are created in one request, so the
	// task never exists with half its rows posed.
	task, _, err := admin.CreateTask(ctx, model.CreateTaskInput{
		Project: "dec", Title: "Settle the storage shape", Priority: "high", Kind: "decision",
		Decisions: []model.Decision{
			{
				Key: "storage", Question: "Where does the index live?",
				ResponseType: "single_select",
				Options: []model.DecisionOption{
					{Label: "Postgres"}, {Label: "S3"},
				},
			},
			{
				Key: "rollout", Question: "How does it ship?",
				ResponseType: "single_select",
				Options: []model.DecisionOption{
					{Label: "All at once"}, {Label: "Per project"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create decision task: %v", err)
	}
	if task.State != "ready" {
		t.Fatalf("created decision task state = %q, want ready", task.State)
	}

	// 2. Both rows read back unanswered, in the order they were authored.
	rows, _, err := dana.ListDecisions(ctx, task.ID)
	if err != nil {
		t.Fatalf("list decisions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("listed %d decisions, want 2: %+v", len(rows), rows)
	}
	if rows[0].Key != "storage" || rows[1].Key != "rollout" {
		t.Fatalf("decision order = %q, %q, want storage, rollout", rows[0].Key, rows[1].Key)
	}
	for _, d := range rows {
		if d.Answer != nil || d.DecidedBy != "" || d.DecidedAt != nil {
			t.Fatalf("decision %s reads as answered before anyone decided: %+v", d.Key, d)
		}
		if len(d.Options) != 2 {
			t.Fatalf("decision %s offers %d options, want 2: %+v", d.Key, len(d.Options), d.Options)
		}
	}

	// 3. A decision is never handed out as work and never leased (004 §6.3 as
	// amended): the ranked pickup loop does not see it, and a claim by id is
	// refused. The store holds nothing else, so "no-ready-task" is the whole
	// ready set answering.
	dry, _, err := dana.ClaimNext(ctx, model.ClaimNextInput{DryRun: true})
	if err != nil {
		t.Fatalf("claim-next dry run: %v", err)
	}
	if dry.Claimed || dry.Task != nil || dry.Reason != "no-ready-task" {
		t.Fatalf("claim-next dry run = %+v, want unclaimed with reason no-ready-task", dry)
	}
	_, _, err = dana.ClaimTask(ctx, task.ID, "h:/.worktrees/0", 0)
	if err == nil {
		t.Fatalf("claim on a decision task: want error, got success")
	}
	var clientErr *cli.ClientError
	if !errors.As(err, &clientErr) {
		t.Fatalf("claim on a decision task: error = %v, want *cli.ClientError", err)
	}
	if clientErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("claim on a decision task: status = %d, want 422", clientErr.Status)
	}
	if !strings.Contains(clientErr.Msg, "is a decision and cannot be claimed") {
		t.Fatalf("claim on a decision task: body = %q, want it to name the decision guard", clientErr.Msg)
	}

	// 4. The assignee is the accountable decider (029 §6.1).
	assigned, _, err := admin.AssignTask(ctx, task.ID, "dana")
	if err != nil {
		t.Fatalf("assign task to dana: %v", err)
	}
	if assigned.Assignee != "dana" {
		t.Fatalf("assignee = %q, want dana", assigned.Assignee)
	}

	// 5. The first answer leaves a row still open, so the task stays ready.
	if _, _, err := dana.AnswerDecision(ctx, task.ID, "storage",
		model.DecisionAnswer{Picked: []string{"Postgres"}}); err != nil {
		t.Fatalf("answer storage: %v", err)
	}
	mid, _, err := dana.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task after the first answer: %v", err)
	}
	if mid.State != "ready" {
		t.Fatalf("task state after the first answer = %q, want ready", mid.State)
	}

	// 6. The last answer carries its own provenance and closes the task in
	// the same write.
	last, _, err := dana.AnswerDecision(ctx, task.ID, "rollout",
		model.DecisionAnswer{Picked: []string{"Per project"}})
	if err != nil {
		t.Fatalf("answer rollout: %v", err)
	}
	if last.Answer == nil || len(last.Answer.Picked) != 1 || last.Answer.Picked[0] != "Per project" {
		t.Fatalf("rollout answer = %+v, want picked [Per project]", last.Answer)
	}
	if last.DecidedBy != "dana" {
		t.Fatalf("rollout decided_by = %q, want dana", last.DecidedBy)
	}
	if last.DecidedAt == nil || last.DecidedAt.IsZero() {
		t.Fatalf("rollout decided_at = %v, want a timestamp", last.DecidedAt)
	}
	closed, _, err := dana.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task after the last answer: %v", err)
	}
	if closed.State != "merged" {
		t.Fatalf("task state after the last answer = %q, want merged", closed.State)
	}

	// 7. Recording is terminal: neither row takes a second answer.
	for key, pick := range map[string]string{"storage": "S3", "rollout": "All at once"} {
		_, _, err := dana.AnswerDecision(ctx, task.ID, key,
			model.DecisionAnswer{Picked: []string{pick}})
		if err == nil {
			t.Fatalf("second answer on %s: want error, got success", key)
		}
		if !errors.As(err, &clientErr) {
			t.Fatalf("second answer on %s: error = %v, want *cli.ClientError", key, err)
		}
		if clientErr.Status != http.StatusConflict {
			t.Fatalf("second answer on %s: status = %d, want 409", key, clientErr.Status)
		}
	}
}
