//go:build e2e

// ladder_test.go drives spec 025 §8's escalation ladder end to end over
// public surfaces only: the gap and fix rungs with their funnel events, an
// escalation and the second worker that joins it rather than minting a rival,
// an amendment that leaves an unexecuted covering plan stale and the re-plan
// task that follows, the withdrawal that resolves a document, and the per-tier
// kind filter a worker loop claims under (§8.8).
//
// §8.7's idle sweeper is deliberately absent. sweepStaleDocs runs only on
// store.StartLeaseSweeper's 60-second tick, which internal/serverapp starts
// and no HTTP surface reaches, so an e2e run cannot provoke a clock-caused
// doc.stale without sleeping out that tick. internal/store/docgroom_test.go
// covers it; what is reachable here is the §8.6 amendment path, which mints
// the groom task through the same rule.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// The three funnel event types 025 §15.5 names. Spelled out rather than
// imported for the reason docLifecycleSubscriberName is: they are the labels
// an operator graphs the ladder by, so this test must fail if one changes.
const (
	typeGapFound    = "task.gap_found"
	typeFixStarted  = "fix.started"
	typeFixFinished = "fix.finished"
)

// ladderSpecBody is the spec both plans cover. Two anchored sections, so an
// amendment can move one while the other stays put.
const ladderSpecBody = `---
status: draft
---

# Ladder Spec

Intro.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.
`

// ladderSpecAmended is that spec with sec-2's prose reworded and nothing
// else: no anchor moves, and none of §8.3's mechanical surfaces (terms, code
// spans, acceptance criteria) are touched, so the patch reaches the caller's
// own judgment rather than a refusal.
const ladderSpecAmended = `---
status: draft
---

# Ladder Spec

Intro.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body, with the stray word removed.
`

// planOneBody covers sec-1 and declares the two tasks the two workers claim.
const planOneBody = `---
status: draft
covers:
  - ladder-spec.md#sec-1
---

# Plan One

## Tasks

### Task 1 — Build the widget

` + "```yaml" + `
kind: feature
priority: high
` + "```" + `

Widget prose.

### Task 2 — Wire the widget

` + "```yaml" + `
kind: chore
priority: low
` + "```" + `

Wiring prose.
`

// planTwoBody covers sec-2, the section the amendment moves. Nobody claims
// its task, which is what makes it §8.6's unexecuted covering plan.
const planTwoBody = `---
status: draft
covers:
  - ladder-spec.md#sec-2
---

# Plan Two

## Tasks

### Task 1 — Model the thing

` + "```yaml" + `
kind: feature
priority: high
` + "```" + `

Modelling prose.
`

// planThreeBody is the plan a grooming pass closes: accepted, covering a
// section the amendment left alone, and never executed.
const planThreeBody = `---
status: draft
covers:
  - ladder-spec.md#sec-1
---

# Plan Three

## Tasks

### Task 1 — Tidy up

` + "```yaml" + `
kind: chore
priority: low
` + "```" + `

Tidying prose.
`

// claimLoop is one worker loop's whole run: claim the highest-ranked task
// matching kinds, close it, repeat until nothing is left. It returns the ids
// it was handed, in claim order. Closing is what moves the loop on — a
// released task goes straight back to the top of the ready set it was just
// picked from.
func claimLoop(t *testing.T, ctx context.Context, c *cli.Client, project, kinds string) []string {
	t.Helper()
	var got []string
	for i := range 10 {
		resp, _, err := c.ClaimNext(ctx, model.ClaimNextInput{
			Project: project, Kind: kinds, Worktree: fmt.Sprintf("wt-%s-%d", kinds, i),
		})
		if err != nil {
			t.Fatalf("claim-next --kind %s: %v", kinds, err)
		}
		if !resp.Claimed {
			return got
		}
		got = append(got, resp.Task.ID)
		if _, _, err := c.AbandonTask(ctx, resp.Task.ID); err != nil {
			t.Fatalf("close %s: %v", resp.Task.ID, err)
		}
	}
	t.Fatalf("claim-next --kind %s claimed 10 tasks without running dry: %v", kinds, got)
	return nil
}

// taskKind reads one task's kind back through the API — ClaimNextPick is the
// ranking projection and carries none.
func taskKind(t *testing.T, ctx context.Context, c *cli.Client, id string) string {
	t.Helper()
	d, _, err := c.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("get task %s: %v", id, err)
	}
	return d.Kind
}

// TestEscalationLadder walks one spec, three plans and two workers through
// spec 025 §8.
func TestEscalationLadder(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)

	// The doc-lifecycle loop holds a pooled connection for its advisory lock,
	// so it must stop before the database goes away. Cleanups run LIFO and
	// OpenTestStore registered the drop first.
	loopCtx, cancelLoop := context.WithCancel(context.Background())
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		BackgroundCtx:  loopCtx,
		EventPoll:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		cancelLoop()
		srv.Close()
	})

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "ladder", Name: "Ladder", Key: "LAD",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	// alice authors the documents; bob and carol execute them. All three are
	// crew: §8.1.4 puts the escalation's fix in the author's queue only when
	// the author is on the project's crew (029 §6.1).
	clients := map[string]*cli.Client{}
	for _, id := range []string{"alice", "bob", "carol"} {
		if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
			ID: id, Kind: "human", DisplayName: id,
		}); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
		if _, _, err := admin.AddCrewMember(ctx, "ladder", id, "member", false, false); err != nil {
			t.Fatalf("add %s to the crew: %v", id, err)
		}
		tok, _, err := admin.CreateToken(ctx, id, "e2e escalation ladder", nil)
		if err != nil {
			t.Fatalf("create token for %s: %v", id, err)
		}
		clients[id] = cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})
	}
	alice, bob, carol := clients["alice"], clients["bob"], clients["carol"]

	// 1. An accepted spec, an accepted plan covering one of its sections, and
	// the task set that plan minted. bob claims the first of them.
	spec, _, err := alice.CreateDoc(ctx, model.CreateDocInput{
		Project: "ladder", Kind: "spec", Number: 1, Slug: "ladder-spec",
		Body: ladderSpecBody, Owner: "alice",
	})
	if err != nil {
		t.Fatalf("create the spec: %v", err)
	}
	if _, _, err := alice.AcceptDoc(ctx, spec.ID); err != nil {
		t.Fatalf("accept the spec: %v", err)
	}
	planOne, _, err := alice.CreateDoc(ctx, model.CreateDocInput{
		Project: "ladder", Kind: "plan", Slug: "plan-one",
		Body: planOneBody, Owner: "alice",
	})
	if err != nil {
		t.Fatalf("create plan one: %v", err)
	}
	acceptOne, _, err := alice.AcceptDoc(ctx, planOne.ID)
	if err != nil {
		t.Fatalf("accept plan one: %v", err)
	}
	if len(acceptOne.Tasks) != 2 {
		t.Fatalf("plan one minted %s, want its two declared tasks", describeTasks(acceptOne.Tasks))
	}
	widget, wiring := acceptOne.Tasks[0], acceptOne.Tasks[1]
	if widget.PlanDoc != planOne.ID {
		t.Fatalf("minted task %s plan_doc = %d, want %d", widget.ID, widget.PlanDoc, planOne.ID)
	}
	if _, _, err := bob.ClaimTask(ctx, widget.ID, "wt-bob", 0); err != nil {
		t.Fatalf("bob claims %s: %v", widget.ID, err)
	}

	// 2. The ladder's three rungs from inside bob's claim. gap and fix write
	// nothing but their events (§15.5); escalate releases the lease, mints the
	// design task that owes the fix, and blocks the task on it (§8.1).
	gap := model.GapTaskInput{
		Doc: "ladder-spec", Anchor: "sec-2",
		Reason: "the model section says nothing about the empty case",
	}
	recorded, _, err := bob.RecordGap(ctx, widget.ID, gap)
	if err != nil {
		t.Fatalf("record gap on %s: %v", widget.ID, err)
	}
	if !recorded.Recorded {
		t.Fatal("first gap report recorded nothing, want a new event")
	}
	// The external id is (task, doc, anchor), so a retry is a no-op rather
	// than a second entry on the funnel.
	replayed, _, err := bob.RecordGap(ctx, widget.ID, gap)
	if err != nil {
		t.Fatalf("replay the gap report: %v", err)
	}
	if replayed.Recorded {
		t.Fatal("the replayed gap report logged a second event, want it absorbed")
	}
	if _, _, err := bob.RecordFix(ctx, widget.ID, model.FixTaskInput{
		Phase: "started", Tier: "plan", Doc: "plan-one", Attempt: 1,
	}); err != nil {
		t.Fatalf("record fix started on %s: %v", widget.ID, err)
	}
	if _, _, err := bob.RecordFix(ctx, widget.ID, model.FixTaskInput{
		Phase: "finished", Outcome: "escalated", Attempt: 1,
	}); err != nil {
		t.Fatalf("record fix finished on %s: %v", widget.ID, err)
	}

	// The funnel, in order. Each type is polled on its own because the events
	// API filters by type and nothing else; the ids are what say they landed
	// in the order the ladder climbs.
	gapEvent := pollEventListE2E(t, ctx, admin, cli.EventListFilter{Type: typeGapFound}, 1)[0]
	startedEvent := pollEventListE2E(t, ctx, admin, cli.EventListFilter{Type: typeFixStarted}, 1)[0]
	finishedEvent := pollEventListE2E(t, ctx, admin, cli.EventListFilter{Type: typeFixFinished}, 1)[0]
	if !(gapEvent.ID < startedEvent.ID && startedEvent.ID < finishedEvent.ID) {
		t.Fatalf("ladder events landed out of order: %s=%d %s=%d %s=%d",
			typeGapFound, gapEvent.ID, typeFixStarted, startedEvent.ID,
			typeFixFinished, finishedEvent.ID)
	}
	if got := eventPayload(t, gapEvent)["reason"]; got != gap.Reason {
		t.Fatalf("%s payload reason = %v, want the reason the fixer reads", typeGapFound, got)
	}
	if got := eventPayload(t, finishedEvent)["outcome"]; got != "escalated" {
		t.Fatalf("%s payload outcome = %v, want escalated", typeFixFinished, got)
	}

	escalation, _, err := bob.EscalateTask(ctx, widget.ID, model.EscalateTaskInput{
		To: "plan", Anchor: "sec-2", Reason: "plan one cannot be executed as written",
	})
	if err != nil {
		t.Fatalf("escalate %s: %v", widget.ID, err)
	}
	if escalation.Minted == nil || escalation.Joined != "" {
		t.Fatalf("escalation result = %+v, want a minted design task and no join", escalation)
	}
	design := *escalation.Minted
	if design.Kind != "design" || design.State != "ready" {
		t.Fatalf("escalation task %s = kind %q state %q, want a ready design task",
			design.ID, design.Kind, design.State)
	}
	if design.AboutDoc != planOne.ID || design.AboutAnchor != "sec-2" {
		t.Fatalf("escalation task %s = about_doc %d anchor %q, want plan one's sec-2",
			design.ID, design.AboutDoc, design.AboutAnchor)
	}
	if design.Assignee != "alice" {
		t.Fatalf("escalation task %s assignee = %q, want alice, who wrote the plan (§8.1.4)",
			design.ID, design.Assignee)
	}
	if !strings.HasPrefix(design.Title, "Gap in ") || !strings.Contains(design.Title, "§2") {
		t.Fatalf("escalation task title = %q, want it to name the document and section", design.Title)
	}

	// The escalating task is back in the pool and held out of it by the edge:
	// 004 has no blocked state, blockedness is the 'blocks' edge, and that is
	// what `lode show` reports.
	widgetDetail, _, err := bob.GetTask(ctx, widget.ID)
	if err != nil {
		t.Fatalf("get task %s: %v", widget.ID, err)
	}
	if !widgetDetail.Blocked {
		t.Fatalf("task %s is not blocked after escalating: %+v", widget.ID, widgetDetail.Edges)
	}
	if widgetDetail.State != "ready" || widgetDetail.Lease != nil {
		t.Fatalf("task %s = state %q lease %+v, want ready with the lease released",
			widget.ID, widgetDetail.State, widgetDetail.Lease)
	}

	// 3. A second worker hits the same gap. §8.1.5 joins the open escalation
	// rather than minting a rival, and blocks this task on it too.
	if _, _, err := carol.ClaimTask(ctx, wiring.ID, "wt-carol", 0); err != nil {
		t.Fatalf("carol claims %s: %v", wiring.ID, err)
	}
	second, _, err := carol.EscalateTask(ctx, wiring.ID, model.EscalateTaskInput{
		To: "plan", Anchor: "sec-2", Reason: "same gap, second worker",
	})
	if err != nil {
		t.Fatalf("carol escalates %s: %v", wiring.ID, err)
	}
	if second.Joined != design.ID || second.Minted != nil {
		t.Fatalf("second escalation = %+v, want it joined to %s", second, design.ID)
	}
	if tasks := tasksAboutDoc(t, ctx, admin, planOne.ID, "design"); len(tasks) != 1 {
		t.Fatalf("design tasks about plan one = %s, want only %s", describeTasks(tasks), design.ID)
	}

	// 4. §8.6: an amendment on a section a second, unexecuted plan covers
	// leaves that plan stale, and the doc.stale event mints the re-plan task.
	planTwo, _, err := alice.CreateDoc(ctx, model.CreateDocInput{
		Project: "ladder", Kind: "plan", Slug: "plan-two",
		Body: planTwoBody, Owner: "alice",
	})
	if err != nil {
		t.Fatalf("create plan two: %v", err)
	}
	acceptTwo, _, err := alice.AcceptDoc(ctx, planTwo.ID)
	if err != nil {
		t.Fatalf("accept plan two: %v", err)
	}
	if len(acceptTwo.Tasks) != 1 {
		t.Fatalf("plan two minted %s, want its one declared task", describeTasks(acceptTwo.Tasks))
	}
	modelling := acceptTwo.Tasks[0]

	patched, _, err := alice.PatchDoc(ctx, spec.ID, model.PatchDocInput{
		Body: ladderSpecAmended, Note: "Dropped a stray word from the model section.",
	})
	if err != nil {
		t.Fatalf("amend the spec: %v", err)
	}
	if !slices.Equal(patched.Patch.ChangedAnchors, []string{"sec-2"}) {
		t.Fatalf("changed anchors = %v, want [sec-2]", patched.Patch.ChangedAnchors)
	}
	if !slices.Equal(patched.Patch.UnexecutedCoveringPlans, []int64{planTwo.ID}) {
		t.Fatalf("unexecuted covering plans = %v, want [%d] (plan one is executed and covers sec-1)",
			patched.Patch.UnexecutedCoveringPlans, planTwo.ID)
	}

	pollDocLifecycleCaughtUp(t, ctx, admin, "after the amendment")
	staleDoc, _, err := admin.GetDoc(ctx, planTwo.ID)
	if err != nil {
		t.Fatalf("get plan two: %v", err)
	}
	if staleDoc.Status != "stale" {
		t.Fatalf("plan two status = %q, want stale", staleDoc.Status)
	}
	plans, _, err := admin.ListDocs(ctx, cli.DocListFilter{Project: "ladder", Kind: "plan"})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	for _, d := range plans.Docs {
		if d.ID == planTwo.ID && d.Status != "stale" {
			t.Fatalf("plan two reads %q in the listing, want stale", d.Status)
		}
	}
	replan := pollTasksAboutDoc(t, ctx, admin, admin, planTwo.ID, "design", 1, "after the amendment")[0]
	if want := "Re-plan: " + staleDoc.Title; replan.Title != want {
		t.Fatalf("groom task title = %q, want %q", replan.Title, want)
	}
	if replan.State != "ready" || replan.CreatedBy != "watcher" {
		t.Fatalf("groom task %s = state %q created_by %q, want a ready watcher mint",
			replan.ID, replan.State, replan.CreatedBy)
	}

	// The stale plan is a flag on the claim, never a refusal: its task claims
	// fine and both the claim response and the brief say the text may predate
	// the amendment.
	claim, _, err := carol.ClaimTask(ctx, modelling.ID, "wt-carol-stale", 0)
	if err != nil {
		t.Fatalf("claim %s from the stale plan: %v", modelling.ID, err)
	}
	if claim.StalePlan != "plan-two" {
		t.Fatalf("claim of %s reports stale_plan %q, want plan-two", modelling.ID, claim.StalePlan)
	}
	brief, _, err := carol.Brief(ctx, modelling.ID)
	if err != nil {
		t.Fatalf("brief for %s: %v", modelling.ID, err)
	}
	if brief.StalePlan != "plan-two" {
		t.Fatalf("brief for %s reports stale_plan %q, want plan-two", modelling.ID, brief.StalePlan)
	}
	var warning bytes.Buffer
	cli.StalePlanWarning(&warning, claim.StalePlan)
	if !strings.Contains(warning.String(), "plan-two") {
		t.Fatalf("claim-time warning = %q, want it to name the stale plan", warning.String())
	}

	// 5. §8.7's idle sweeper is out of reach from here — see this file's
	// header. The mint it shares with §8.6 is what step 4 just exercised.

	// 6. Grooming closes what will not be executed. An accepted plan nobody
	// has claimed work from is unresolved; withdrawing it takes it out of that
	// set, which is what makes full resolution reachable.
	planThree, _, err := alice.CreateDoc(ctx, model.CreateDocInput{
		Project: "ladder", Kind: "plan", Slug: "plan-three",
		Body: planThreeBody, Owner: "alice",
	})
	if err != nil {
		t.Fatalf("create plan three: %v", err)
	}
	if _, _, err := alice.AcceptDoc(ctx, planThree.ID); err != nil {
		t.Fatalf("accept plan three: %v", err)
	}
	unresolved, _, err := admin.ListDocs(ctx, cli.DocListFilter{Project: "ladder", Unresolved: true})
	if err != nil {
		t.Fatalf("list unresolved docs: %v", err)
	}
	if len(unresolved.Docs) != 1 || unresolved.Docs[0].ID != planThree.ID {
		t.Fatalf("unresolved docs = %+v, want only plan three", unresolved.Docs)
	}
	withdrawn, _, err := alice.WithdrawDoc(ctx, planThree.ID, "nothing will execute it")
	if err != nil {
		t.Fatalf("withdraw plan three: %v", err)
	}
	if withdrawn.Status != "withdrawn" {
		t.Fatalf("plan three status = %q, want withdrawn", withdrawn.Status)
	}
	unresolved, _, err = admin.ListDocs(ctx, cli.DocListFilter{Project: "ladder", Unresolved: true})
	if err != nil {
		t.Fatalf("list unresolved docs after the withdrawal: %v", err)
	}
	if len(unresolved.Docs) != 0 {
		t.Fatalf("unresolved docs after withdrawing plan three = %+v, want none", unresolved.Docs)
	}

	// 7. §8.8: a loop restricted to the design tier picks the escalation up,
	// and a loop restricted to the execution tier never sees it.
	designed := claimLoop(t, ctx, alice, "ladder", "design,review")
	if !slices.Contains(designed, design.ID) {
		t.Fatalf("the design loop claimed %v, want the escalation task %s among them",
			designed, design.ID)
	}
	for _, id := range designed {
		if kind := taskKind(t, ctx, admin, id); kind != "design" && kind != "review" {
			t.Fatalf("the design loop claimed %s, a %s task", id, kind)
		}
	}
	for _, id := range claimLoop(t, ctx, alice, "ladder", "feature,bug,chore") {
		kind := taskKind(t, ctx, admin, id)
		if kind == "design" || kind == "review" {
			t.Fatalf("the execution loop claimed %s, a %s task", id, kind)
		}
	}
}
