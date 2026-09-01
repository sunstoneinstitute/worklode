//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// specSourceBody is the draft spec actor A creates: real frontmatter, an H1,
// and two anchored sections whose anchors agree with their numbers (the
// server parses and lints this, per 025 §5/§6.1).
const specSourceBody = `---
status: draft
---

# Test Spec

Intro.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.
`

// specRevisedBody is specSourceBody with sec-1a inserted and sec-2's body
// edited; sec-1 is left untouched. This is the revision that must be
// accepted: exactly one Added anchor and one Changed anchor (025 §6 rule 5).
const specRevisedBody = `---
status: accepted
---

# Test Spec

Intro.

## 1. Scope {#sec-1}

Scope body.

## 1a. Extra {#sec-1a}

Extra body.

## 2. Model {#sec-2}

Model body, revised.
`

// planSourceBody is a plan doc: no corpus number, no anchored sections
// (025 §9). It deliberately declares no `## Tasks` section, so accepting it
// is refused — a plan mints its tasks on accept, and a body that declares
// none is a plan with nothing to execute (025 §9.2).
const planSourceBody = `---
status: draft
---

# Test Plan

## Task 1

Do the thing.
`

// planAlphaBody is the earlier of the two ordered plans: one task, and no
// ordering key of its own — plan-beta declares the order (025 §5, §9.3).
const planAlphaBody = `---
status: draft
---

# Alpha Plan

## Tasks

### Task 1 — Land the groundwork

` + "```yaml" + `
kind: chore
priority: low
` + "```" + `

Groundwork prose.
`

// planBetaBody is the plan under test: two task definitions, the second
// declaring skills and an intra-plan blockedBy on the first (025 §9.1), under
// a document-level `blockedBy` holding this plan's whole set until plan-alpha's
// closes (025 §5, §9.3).
//
// The two `blockedBy` keys are different subjects that happen to share a
// spelling: the frontmatter one names another plan document, the one in a task
// block names a task number in this file (025 §9.1).
const planBetaBody = `---
status: draft
blockedBy:
  - plan-alpha
---

# Beta Plan

## Tasks

### Task 1 — Build the widget

` + "```yaml" + `
kind: feature
priority: high
` + "```" + `

Widget prose.

### Task 2 — Test the widget

` + "```yaml" + `
kind: bug
priority: critical
skills:
  - superpowers:test-driven-development
  - superpowers:verification-before-completion
blockedBy: [1]
` + "```" + `

Test prose.
`

// findDocSection returns the section with the given anchor, or nil.
func findDocSection(secs []model.DocSection, anchor string) *model.DocSection {
	for i := range secs {
		if secs[i].Anchor == anchor {
			return &secs[i]
		}
	}
	return nil
}

// clientErrStatus unwraps a *cli.ClientError's HTTP status from err, failing
// the test if err is not one (every non-2xx response from the server comes
// back as one, per cli.Client.do).
func clientErrStatus(t *testing.T, err error) int {
	t.Helper()
	var ce *cli.ClientError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want a *cli.ClientError", err, err)
	}
	return ce.Status
}

// TestDocLifecycle drives spec 025's document lifecycle through the public
// HTTP API only: a spec's draft -> accept -> revise -> accept-revision path,
// its owner gate and its §6 anchor rules, and a plan's freely-editable
// body with acceptance still stubbed out (025 §9.2, lifted in part 3).
func TestDocLifecycle(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		WebOpen:        true,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// 1. Project and two actors, each with its own token: actor A is the
	// spec's owner, actor B is not.
	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "docs", Name: "Docs E2E", Key: "DOCS",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	for _, id := range []string{"actor-a", "actor-b"} {
		if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
			ID: id, Kind: "human", DisplayName: id,
		}); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}
	tokA, _, err := admin.CreateToken(ctx, "actor-a", "e2e doc lifecycle", nil)
	if err != nil {
		t.Fatalf("create token for actor-a: %v", err)
	}
	tokB, _, err := admin.CreateToken(ctx, "actor-b", "e2e doc lifecycle", nil)
	if err != nil {
		t.Fatalf("create token for actor-b: %v", err)
	}
	actorA := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tokA.Token})
	actorB := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tokB.Token})

	// 2. Actor A creates a spec draft with two anchored sections, assigned to
	// itself — the only actor that can accept it (025 §7).
	doc, _, err := actorA.CreateDoc(ctx, model.CreateDocInput{
		Project: "docs", Kind: "spec", Number: 1, Slug: "test-spec",
		Body: specSourceBody, Owner: "actor-a",
	})
	if err != nil {
		t.Fatalf("create spec: %v", err)
	}
	if doc.Status != "draft" || doc.Owner != "actor-a" {
		t.Fatalf("created doc = %+v, want draft assigned to actor-a", doc)
	}

	// 3. Actor B's accept fails: the owner gate is 403, not a generic
	// error.
	if _, _, err := actorB.AcceptDoc(ctx, doc.ID); err == nil {
		t.Fatal("actor-b accept: want an error, got nil")
	} else if status := clientErrStatus(t, err); status != http.StatusForbidden {
		t.Fatalf("actor-b accept: status = %d, want 403 (err %v)", status, err)
	}

	// 4. Actor A accepts: status flips to accepted and both sections publish.
	accepted, _, err := actorA.AcceptDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("actor-a accept: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Fatalf("accepted doc status = %q, want accepted", accepted.Status)
	}
	detail, _, err := actorA.GetDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get doc after accept: %v", err)
	}
	sec1 := findDocSection(detail.Sections, "sec-1")
	sec2 := findDocSection(detail.Sections, "sec-2")
	if sec1 == nil || sec2 == nil {
		t.Fatalf("sections after accept = %+v, want sec-1 and sec-2", detail.Sections)
	}
	if !sec1.Published || !sec2.Published {
		t.Fatalf("sections after accept = %+v, want both published", detail.Sections)
	}

	// 5. A revision is structurally a pull request (025 §7.2): actor B may
	// open one against actor A's document even though B cannot accept it, and
	// A — the owner — may withdraw it without landing anything, freeing the
	// one-candidate slot for the steps below.
	if _, _, err := actorB.ReviseDoc(ctx, doc.ID); err != nil {
		t.Fatalf("revise doc as a non-owner: %v", err)
	}
	if _, _, err := actorA.DiscardDocRevision(ctx, doc.ID); err != nil {
		t.Fatalf("discard a non-owner's revision as the owner: %v", err)
	}
	if _, _, err := actorA.DiscardDocRevision(ctx, doc.ID); err == nil {
		t.Fatal("discard with nothing open: want an error, got nil")
	} else if status := clientErrStatus(t, err); status != http.StatusNotFound {
		t.Fatalf("discard with nothing open: status = %d, want 404 (err %v)", status, err)
	}

	// 6. Open a revision, then edit it to drop sec-2's number while keeping
	// its anchor: this is the form that reaches the diff (renumbering while
	// keeping the anchor is a lintAnchors defect refused at parse time), and
	// AcceptDocRevision must reject it citing 025 §6 rule 3.
	if _, _, err := actorA.ReviseDoc(ctx, doc.ID); err != nil {
		t.Fatalf("revise doc: %v", err)
	}
	droppedNumberBody := strings.Replace(specSourceBody, "## 2. Model {#sec-2}", "## Model {#sec-2}", 1)
	if _, _, err := actorA.UpdateDocRevision(ctx, doc.ID, droppedNumberBody); err != nil {
		t.Fatalf("update revision (dropped number): %v", err)
	}
	if _, _, err := actorA.AcceptDocRevision(ctx, doc.ID); err == nil {
		t.Fatal("accept revision with dropped number: want an error, got nil")
	} else {
		if status := clientErrStatus(t, err); status != http.StatusUnprocessableEntity {
			t.Fatalf("accept revision with dropped number: status = %d, want 422 (err %v)", status, err)
		}
		if !strings.Contains(err.Error(), `sec-2: renumbered from "2" to ""`) ||
			!strings.Contains(err.Error(), "025 §6 rule 3") {
			t.Fatalf("accept revision with dropped number: err = %v, want the rule 3 violation naming sec-2", err)
		}
	}

	// 7. Actor B is neither the owner nor this candidate's author, so B
	// cannot withdraw it either — the discard gate is the pair, not doc.write.
	if _, _, err := actorB.DiscardDocRevision(ctx, doc.ID); err == nil {
		t.Fatal("third-party discard: want an error, got nil")
	} else if status := clientErrStatus(t, err); status != http.StatusForbidden {
		t.Fatalf("third-party discard: status = %d, want 403 (err %v)", status, err)
	}

	// 8. Replace the open revision with one that adds sec-1a and edits only
	// sec-2's body: this must be accepted, landing as version 2 with
	// last_revised_in moved on exactly sec-2 (025 §6 rule 5).
	if _, _, err := actorA.UpdateDocRevision(ctx, doc.ID, specRevisedBody); err != nil {
		t.Fatalf("update revision (valid): %v", err)
	}
	landed, _, err := actorA.AcceptDocRevision(ctx, doc.ID)
	if err != nil {
		t.Fatalf("accept valid revision: %v", err)
	}
	if landed.Version != 2 {
		t.Fatalf("landed doc version = %d, want 2", landed.Version)
	}
	detail, _, err = actorA.GetDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get doc after revision accept: %v", err)
	}
	if detail.Version != 2 {
		t.Fatalf("doc version after revision accept = %d, want 2", detail.Version)
	}
	if len(detail.Sections) != 3 {
		t.Fatalf("sections after revision accept = %+v, want exactly 3 (sec-1, sec-1a, sec-2)", detail.Sections)
	}
	sec1 = findDocSection(detail.Sections, "sec-1")
	sec1a := findDocSection(detail.Sections, "sec-1a")
	sec2 = findDocSection(detail.Sections, "sec-2")
	if sec1 == nil || sec1a == nil || sec2 == nil {
		t.Fatalf("sections after revision accept = %+v, want sec-1, sec-1a and sec-2", detail.Sections)
	}
	if sec1.LastRevisedIn != 1 {
		t.Fatalf("sec-1 last_revised_in = %d, want 1 (untouched by the revision)", sec1.LastRevisedIn)
	}
	if sec2.LastRevisedIn != 2 {
		t.Fatalf("sec-2 last_revised_in = %d, want 2 (its body was edited)", sec2.LastRevisedIn)
	}
	if sec1a.LastRevisedIn != 2 {
		t.Fatalf("sec-1a last_revised_in = %d, want 2 (introduced in this revision)", sec1a.LastRevisedIn)
	}
	if !sec1a.Published {
		t.Fatalf("sec-1a published = false, want true (accept publishes every current anchor)")
	}

	// 9. The plan half: a plan carries a server-allocated number like every
	// other kind (029 §4) but no anchors, and its body is freely editable at
	// any status. Accepting this one is refused because it declares no
	// `## Tasks` section — plan acceptance mints the plan's tasks (025 §9.2),
	// and a plan that would mint nothing is not acceptable.
	// TestPlanAcceptanceMintsTasks drives the accepting path.
	plan, _, err := actorA.CreateDoc(ctx, model.CreateDocInput{
		Project: "docs", Kind: "plan", Slug: "test-plan",
		Body: planSourceBody, Owner: "actor-a",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if plan.Number != 1 {
		t.Fatalf("plan number = %d, want 1 (029 §4, first plan in project)", plan.Number)
	}
	editedPlanBody := strings.Replace(planSourceBody, "Do the thing.", "Do the other thing.", 1)
	if _, _, err := actorA.UpdateDocBody(ctx, plan.ID, editedPlanBody); err != nil {
		t.Fatalf("edit plan body while draft: %v", err)
	}
	if _, _, err := actorA.AcceptDoc(ctx, plan.ID); err == nil {
		t.Fatal("accept taskless plan: want an error, got nil")
	} else {
		if status := clientErrStatus(t, err); status != http.StatusUnprocessableEntity {
			t.Fatalf("accept taskless plan: status = %d, want 422 (err %v)", status, err)
		}
		if !strings.Contains(err.Error(), "plan defines no tasks") {
			t.Fatalf("accept taskless plan: err = %v, want it to name the missing task set", err)
		}
	}
}

// hasDocEdge reports whether edges holds one of typ pointing at doc.
func hasDocEdge(edges []model.DocEdge, typ string, doc int64) bool {
	return slices.ContainsFunc(edges, func(e model.DocEdge) bool {
		return e.Type == typ && e.ToDoc == doc
	})
}

// assertNoChildEdges fails if the task has a parent or any child_of edge in
// either direction: plan acceptance mints a flat set with no row above it
// (025 §9.2).
func assertNoChildEdges(ctx context.Context, t *testing.T, c *cli.Client, id string) {
	t.Helper()
	d, _, err := c.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("get task %s: %v", id, err)
	}
	if d.Hierarchy.Parent != nil {
		t.Fatalf("task %s parent = %+v, want none", id, d.Hierarchy.Parent)
	}
	for _, e := range d.Edges.Out {
		if e.Type == "child_of" {
			t.Fatalf("task %s has a child_of edge to %s, want none", id, e.To)
		}
	}
	for _, e := range d.Edges.In {
		if e.Type == "child_of" {
			t.Fatalf("task %s has a child_of edge from %s, want none", id, e.From)
		}
	}
}

// assertBlocked checks a task's derived blocked flag, the same predicate the
// claim path enforces (store.IsBlocked).
func assertBlocked(ctx context.Context, t *testing.T, c *cli.Client, id string, want bool, why string) {
	t.Helper()
	d, _, err := c.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("get task %s: %v", id, err)
	}
	if d.Blocked != want {
		t.Fatalf("task %s blocked = %v, want %v (%s)", id, d.Blocked, want, why)
	}
}

// assertNeedsExecution runs `lode doc list --needs-execution --json` through
// the real CLI entry point and compares the ids it lists with want.
// LODE_SERVER and LODE_TOKEN must already point at the server under test.
func assertNeedsExecution(t *testing.T, project string, want ...int64) {
	t.Helper()
	out, err := runLodeCLI(t, "doc", "list", "--project", project, "--needs-execution", "--json")
	if err != nil {
		t.Fatalf("lode doc list --needs-execution: %v\noutput: %s", err, out)
	}
	var resp model.DocListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode `lode doc list --needs-execution --json` output %q: %v", out, err)
	}
	got := make([]int64, 0, len(resp.Docs))
	for _, d := range resp.Docs {
		got = append(got, d.ID)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("lode doc list --needs-execution = %v, want %v", got, want)
	}
}

// TestPlanAcceptanceMintsTasks drives 025 §9 end to end through the public
// surfaces: two plan documents ordered by a frontmatter `blockedBy` edge, the
// second declaring two tasks with an intra-plan blockedBy. It proves that
// accepting a plan mints exactly its declared task set and nothing above it,
// that both gates — plan-to-plan and task-to-task — hold before they release,
// and that `lode doc list --needs-execution` tracks the set closing.
func TestPlanAcceptanceMintsTasks(t *testing.T) {
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
		ID: "plans", Name: "Plan Acceptance", Key: "PLN",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "planner", Kind: "agent", DisplayName: "Planner",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "planner", "e2e plan acceptance", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	planner := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	// The --needs-execution assertions go through the real CLI, which reads
	// the server and token from the environment; an empty cwd keeps any
	// repo-local .worklode config out of the resolution.
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", tok.Token)
	t.Chdir(t.TempDir())

	// 1. Both plans, in the order a numbered series is actually written:
	// alpha, then beta declaring `blockedBy: [plan-alpha]`. Either end may
	// state the ordering, and the later plan naming the earlier one is the
	// direction that does not require going back to amend a plan that may
	// already be accepted and spent (025 §5).
	alpha, _, err := planner.CreateDoc(ctx, model.CreateDocInput{
		Project: "plans", Kind: "plan", Slug: "plan-alpha",
		Body: planAlphaBody, Owner: "planner",
	})
	if err != nil {
		t.Fatalf("create plan-alpha: %v", err)
	}
	beta, _, err := planner.CreateDoc(ctx, model.CreateDocInput{
		Project: "plans", Kind: "plan", Slug: "plan-beta",
		Body: planBetaBody, Owner: "planner",
	})
	if err != nil {
		t.Fatalf("create plan-beta: %v", err)
	}

	// 2. Beta's frontmatter wrote one doc_edges row — the one alpha's
	// `blocks: [plan-beta]` would have written — readable from both ends:
	// blocks leaving alpha, blockedBy arriving at beta (025 §5, §14).
	alphaDoc, _, err := planner.GetDoc(ctx, alpha.ID)
	if err != nil {
		t.Fatalf("get plan-alpha: %v", err)
	}
	if !hasDocEdge(alphaDoc.Edges, "blocks", beta.ID) {
		t.Fatalf("plan-alpha edges = %+v, want a blocks edge to doc %d", alphaDoc.Edges, beta.ID)
	}
	betaDoc, _, err := planner.GetDoc(ctx, beta.ID)
	if err != nil {
		t.Fatalf("get plan-beta: %v", err)
	}
	if !hasDocEdge(betaDoc.EdgesIn, "blockedBy", alpha.ID) {
		t.Fatalf("plan-beta inbound edges = %+v, want a blockedBy edge from doc %d", betaDoc.EdgesIn, alpha.ID)
	}

	// 3. Accept alpha: one draft task, minted in the accept transaction.
	alphaAccepted, _, err := planner.AcceptDoc(ctx, alpha.ID)
	if err != nil {
		t.Fatalf("accept plan-alpha: %v", err)
	}
	if alphaAccepted.Status != "accepted" {
		t.Fatalf("plan-alpha status = %q, want accepted", alphaAccepted.Status)
	}
	if len(alphaAccepted.Tasks) != 1 {
		t.Fatalf("plan-alpha minted %d tasks, want 1: %+v", len(alphaAccepted.Tasks), alphaAccepted.Tasks)
	}
	alphaTask := alphaAccepted.Tasks[0]
	if alphaTask.PlanDoc != alpha.ID || alphaTask.State != "draft" || alphaTask.Kind != "chore" {
		t.Fatalf("plan-alpha task = %+v, want a draft chore carrying plan_doc %d", alphaTask, alpha.ID)
	}

	// 4. Accept beta: two draft tasks, in definition order, each carrying its
	// declared metadata.
	betaAccepted, _, err := planner.AcceptDoc(ctx, beta.ID)
	if err != nil {
		t.Fatalf("accept plan-beta: %v", err)
	}
	if len(betaAccepted.Tasks) != 2 {
		t.Fatalf("plan-beta minted %d tasks, want 2: %+v", len(betaAccepted.Tasks), betaAccepted.Tasks)
	}
	first, second := betaAccepted.Tasks[0], betaAccepted.Tasks[1]
	for _, want := range []struct {
		task                  model.Task
		title, kind, priority string
		skills                []string
	}{
		{first, "Build the widget", "feature", "high", nil},
		{second, "Test the widget", "bug", "critical", []string{
			"superpowers:test-driven-development",
			"superpowers:verification-before-completion",
		}},
	} {
		got := want.task
		if got.State != "draft" {
			t.Fatalf("minted task %s state = %q, want draft (025 §9.2)", got.ID, got.State)
		}
		if got.PlanDoc != beta.ID {
			t.Fatalf("minted task %s plan_doc = %d, want %d", got.ID, got.PlanDoc, beta.ID)
		}
		if got.Title != want.title || got.Kind != want.kind || got.Priority != want.priority {
			t.Fatalf("minted task %s = %q/%s/%s, want %q/%s/%s",
				got.ID, got.Title, got.Kind, got.Priority, want.title, want.kind, want.priority)
		}
		// slices.Equal treats the store's guaranteed empty slice and a nil
		// want as equal, which is what "no skills declared" means here.
		if !slices.Equal(got.Skills, want.skills) {
			t.Fatalf("minted task %s skills = %v, want %v", got.ID, got.Skills, want.skills)
		}
	}

	// 5. The intra-plan blockedBy became a task-to-task blocks edge.
	secondDetail, _, err := planner.GetTask(ctx, second.ID)
	if err != nil {
		t.Fatalf("get task %s: %v", second.ID, err)
	}
	if !slices.ContainsFunc(secondDetail.Edges.In, func(e model.TaskEdgeIn) bool {
		return e.Type == "blocks" && e.From == first.ID
	}) {
		t.Fatalf("task %s inbound edges = %+v, want a blocks edge from %s",
			second.ID, secondDetail.Edges.In, first.ID)
	}

	// 6. Nothing else was created: exactly the three declared tasks, no
	// container above them, no child_of anywhere.
	all, _, err := planner.ListTasks(ctx, cli.TaskListFilter{Project: "plans"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	gotIDs := make([]string, 0, len(all.Tasks))
	for _, task := range all.Tasks {
		gotIDs = append(gotIDs, task.ID)
	}
	slices.Sort(gotIDs)
	wantIDs := []string{alphaTask.ID, first.ID, second.ID}
	slices.Sort(wantIDs)
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("project tasks = %v, want exactly the minted set %v", gotIDs, wantIDs)
	}
	containers, _, err := planner.ListTasks(ctx, cli.TaskListFilter{Project: "plans", HasChildren: true})
	if err != nil {
		t.Fatalf("list container tasks: %v", err)
	}
	if len(containers.Tasks) != 0 {
		t.Fatalf("container tasks = %+v, want none (a plan mints no row above its set)", containers.Tasks)
	}
	for _, id := range wantIDs {
		assertNoChildEdges(ctx, t, planner, id)
	}

	// Both plans are accepted with open sets, so both need execution.
	assertNeedsExecution(t, "plans", alpha.ID, beta.ID)

	// 7. Ready the whole set. Beta's tasks are ready and unleased, and would
	// be pickable but for the plan-to-plan edge.
	for _, id := range []string{alphaTask.ID, first.ID, second.ID} {
		if _, _, err := planner.ReadyTask(ctx, id); err != nil {
			t.Fatalf("ready task %s: %v", id, err)
		}
	}
	assertBlocked(ctx, t, planner, first.ID, true, "plan-alpha's set is still open")
	assertBlocked(ctx, t, planner, second.ID, true, "plan-alpha's set is still open")

	// 8. claim --next hands out alpha's task, then finds nothing: beta's two
	// ready tasks are held by the plan-to-plan gate, not merely unreached.
	pick, _, err := planner.ClaimNext(ctx, model.ClaimNextInput{Project: "plans", Worktree: "h:/.worktrees/0"})
	if err != nil {
		t.Fatalf("claim-next #1: %v", err)
	}
	if !pick.Claimed || pick.Task == nil || pick.Task.ID != alphaTask.ID {
		t.Fatalf("claim-next #1 = %+v, want %s", pick, alphaTask.ID)
	}
	held, _, err := planner.ClaimNext(ctx, model.ClaimNextInput{Project: "plans", Worktree: "h:/.worktrees/1"})
	if err != nil {
		t.Fatalf("claim-next while gated: %v", err)
	}
	if held.Claimed || held.Task != nil {
		t.Fatalf("claim-next while gated = %+v, want nothing: plan-beta's set is held by plan-alpha", held)
	}

	// 9. Close alpha's set. The plan gate releases; the intra-plan edge does
	// not, so only beta's task 1 becomes pickable.
	if done, _, err := planner.SetTaskState(ctx, alphaTask.ID, "merged"); err != nil {
		t.Fatalf("done task %s: %v", alphaTask.ID, err)
	} else if done.State != "merged" {
		t.Fatalf("task %s state = %q, want merged", alphaTask.ID, done.State)
	}
	assertBlocked(ctx, t, planner, first.ID, false, "plan-alpha's set has closed")
	assertBlocked(ctx, t, planner, second.ID, true, "task 1 of plan-beta is still open")
	assertNeedsExecution(t, "plans", beta.ID)

	pick, _, err = planner.ClaimNext(ctx, model.ClaimNextInput{Project: "plans", Worktree: "h:/.worktrees/1"})
	if err != nil {
		t.Fatalf("claim-next #2: %v", err)
	}
	if !pick.Claimed || pick.Task == nil || pick.Task.ID != first.ID {
		t.Fatalf("claim-next #2 = %+v, want %s (task 2 is edge-blocked)", pick, first.ID)
	}
	held, _, err = planner.ClaimNext(ctx, model.ClaimNextInput{Project: "plans", Worktree: "h:/.worktrees/2"})
	if err != nil {
		t.Fatalf("claim-next while task 2 is blocked: %v", err)
	}
	if held.Claimed || held.Task != nil {
		t.Fatalf("claim-next while task 2 is blocked = %+v, want nothing", held)
	}

	// 10. Close beta's set, one task at a time; the plan stays in
	// --needs-execution until the last task closes.
	if _, _, err := planner.SetTaskState(ctx, first.ID, "merged"); err != nil {
		t.Fatalf("done task %s: %v", first.ID, err)
	}
	assertNeedsExecution(t, "plans", beta.ID)

	pick, _, err = planner.ClaimNext(ctx, model.ClaimNextInput{Project: "plans", Worktree: "h:/.worktrees/2"})
	if err != nil {
		t.Fatalf("claim-next #3: %v", err)
	}
	if !pick.Claimed || pick.Task == nil || pick.Task.ID != second.ID {
		t.Fatalf("claim-next #3 = %+v, want %s", pick, second.ID)
	}
	if _, _, err := planner.SetTaskState(ctx, second.ID, "merged"); err != nil {
		t.Fatalf("done task %s: %v", second.ID, err)
	}
	assertNeedsExecution(t, "plans")
}

// TestDocOperationsMetricOnMetrics proves worklode_doc_operations_total is
// visible on the admin /metrics endpoint after a real document mutation
// through the public API — the wiring serve.go relies on
// (store.WithMetrics(reg) -> the same registry -> the admin listener's
// /metrics), not just that the counter increments into an in-process
// registry nobody scrapes, which the store unit test already covers.
func TestDocOperationsMetricOnMetrics(t *testing.T) {
	ctx := context.Background()

	reg := prometheus.NewRegistry()
	st := store.OpenTestStore(t, store.WithMetrics(reg))
	main, admin, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		WebOpen:        true,
		Metrics:        reg,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(main)
	defer srv.Close()

	adminClient := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := adminClient.CreateProject(ctx, model.CreateProjectInput{
		ID: "docs-metrics", Name: "Docs Metrics", Key: "DOCM",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := adminClient.CreateDoc(ctx, model.CreateDocInput{
		Project: "docs-metrics", Kind: "plan", Slug: "metrics-plan", Body: planSourceBody,
	}); err != nil {
		t.Fatalf("create doc: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics: status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "worklode_doc_operations_total") {
		t.Fatalf("/metrics missing worklode_doc_operations_total:\n%s", body)
	}
	if want := `worklode_doc_operations_total{op="create",outcome="ok"}`; !strings.Contains(body, want) {
		t.Fatalf("/metrics missing %s; body:\n%s", want, body)
	}
}
