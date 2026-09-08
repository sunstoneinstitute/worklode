//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// progressSpecBodyE2E is a spec with two anchored sections, both of which the
// plan below covers in full.
const progressSpecBodyE2E = `---
status: draft
---

# The widget

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.
`

// progressPlanBodyE2E covers both sections and declares two tasks.
const progressPlanBodyE2E = `---
status: draft
covers:
  - spec: 001-widget.md#sec-1
    coverage: full
  - spec: 001-widget.md#sec-2
    coverage: full
---

# Widget plan

## Tasks

### Task 1 — Build the widget

` + "```yaml" + `
kind: feature
priority: high
blockedBy: []
` + "```" + `

Build it.

### Task 2 — Test the widget

` + "```yaml" + `
kind: feature
priority: medium
blockedBy: []
` + "```" + `

Test it.
`

// TestProgressOverAMintedPlan drives WL-SPEC-66 §8 end to end through public
// surfaces only: a spec and a plan created over the API, the plan accepted so
// it mints its two tasks, one task walked to merged. The spec then sits in
// the "active" group with one open task in the plan, and all three readers —
// the cockpit page, the JSON API and `lode doc progress --json` — say the
// same thing, because each recomputes the same derivation per call. The
// still-open second task then carries a PR into GitHub's merge queue: a
// signed pull_request delivery correlates the PR, and merge_group's
// checks_requested and destroyed actions move its row tip onto and off
// "queued for merge" (WL-SPEC-66 §6, §8).
func TestProgressOverAMintedPlan(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken:      bootstrapToken,
		WebOpen:             true,
		GitHubWebhookSecret: githubSecret,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "prog", Name: "Progress", Key: "PRG",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.AddRepo(ctx, "prog", repo, ""); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "planner", Kind: "agent", DisplayName: "Planner",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "planner", "e2e progress", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	planner := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	// `lode doc progress` reads the server and token from the environment;
	// an empty cwd keeps any repo-local .worklode config out of the scope
	// resolution, which --project stops anyway.
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", tok.Token)
	t.Chdir(t.TempDir())

	// 1. The spec, then the plan covering both of its sections.
	spec, _, err := planner.CreateDoc(ctx, model.CreateDocInput{
		Project: "prog", Kind: "spec", Number: 1, Slug: "001-widget",
		Body: progressSpecBodyE2E, Owner: "planner",
	})
	if err != nil {
		t.Fatalf("create spec: %v", err)
	}
	plan, _, err := planner.CreateDoc(ctx, model.CreateDocInput{
		Project: "prog", Kind: "plan", Slug: "001-widget-plan",
		Body: progressPlanBodyE2E, Owner: "planner",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	// 2. Accepting the plan mints its two tasks; walking one to merged
	// leaves the plan in_progress with a single open task.
	accepted, _, err := planner.AcceptDoc(ctx, plan.ID)
	if err != nil {
		t.Fatalf("accept plan: %v", err)
	}
	if len(accepted.Tasks) != 2 {
		t.Fatalf("plan minted %d tasks, want 2: %+v", len(accepted.Tasks), accepted.Tasks)
	}
	if done, _, err := planner.SetTaskState(ctx, accepted.Tasks[0].ID, "merged"); err != nil {
		t.Fatalf("merge task %s: %v", accepted.Tasks[0].ID, err)
	} else if done.State != "merged" {
		t.Fatalf("task %s state = %q, want merged", done.ID, done.State)
	}

	// 2b. The live stream (WL-743), opened after the fact with ?after=0,
	// replays that transition as a frame naming this spec. ListEvents is
	// bounded by a cluster-wide commit horizon, so a just-recorded event can
	// stay invisible for several polls; the stream correctly emits
	// heartbeats while it waits, so frames are read in a loop rather than
	// trusting the first line, bounded by a timeout matching
	// store.AwaitCommitHorizon's own deadline.
	streamCtx, cancelStream := context.WithTimeout(ctx, 20*time.Second)
	defer cancelStream()
	streamReq, err := http.NewRequestWithContext(streamCtx, http.MethodGet,
		srv.URL+"/projects/prog/progress/events?after=0", nil)
	if err != nil {
		t.Fatalf("build progress events request: %v", err)
	}
	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("GET progress/events: %v", err)
	}
	defer streamResp.Body.Close()
	if streamResp.StatusCode != 200 {
		t.Fatalf("GET progress/events status = %d", streamResp.StatusCode)
	}
	frame := readProgressFrame(t, streamResp.Body, spec.ID, accepted.Tasks[0].ID)
	if frame.Task != accepted.Tasks[0].ID || frame.State != "merged" {
		t.Fatalf("progress frame = %+v, want task %s merged", frame, accepted.Tasks[0].ID)
	}

	// 2c. The row fragment for this spec shows the merged task with a
	// tooltip naming its new position on the ladder.
	fragStatus, fragBody := getPage(t, fmt.Sprintf("%s/projects/prog/progress/spec/%d", srv.URL, spec.ID))
	if fragStatus != 200 {
		t.Fatalf("GET progress/spec/%d status = %d", spec.ID, fragStatus)
	}
	wantTip := fmt.Sprintf(`data-tip="%s · %s · merged"`, accepted.Tasks[0].ID, accepted.Tasks[0].Title)
	if !strings.Contains(fragBody, wantTip) {
		t.Fatalf("progress row fragment missing %q:\n%s", wantTip, fragBody)
	}

	wantNext := fmt.Sprintf("1 open task(s) in %s", plan.Ref)

	// 3. The JSON API: the spec is in the active group with that next act.
	p, _, err := planner.ProjectProgress(ctx, "prog")
	if err != nil {
		t.Fatalf("GET /api/v1/projects/prog/progress: %v", err)
	}
	got := progressSpecByRef(t, p, spec.Ref)
	if got.Group != "active" {
		t.Fatalf("spec %s group = %q, want active", spec.Ref, got.Group)
	}
	if got.Next.Text != wantNext {
		t.Fatalf("spec %s next = %q, want %q", spec.Ref, got.Next.Text, wantNext)
	}
	if p.Counts.Active != 1 {
		t.Fatalf("active count = %d, want 1", p.Counts.Active)
	}

	// 4. The cockpit page draws the same row. Only one group has any spec in
	// it, so the group card the row sits in is the "Active" one.
	status, body := getPage(t, srv.URL+"/projects/prog/progress")
	if status != 200 {
		t.Fatalf("GET /projects/prog/progress status = %d", status)
	}
	active := strings.Index(body, "<h3>Active")
	row := strings.Index(body, ">"+spec.Ref+"</a>")
	if active < 0 || row < active {
		t.Fatalf("progress page does not show %s under Active (heading at %d, row at %d):\n%s",
			spec.Ref, active, row, body)
	}
	if !strings.Contains(body, wantNext) {
		t.Fatalf("progress page missing next act %q:\n%s", wantNext, body)
	}

	// 4b. The spec row offers the Rally button, and both it and the plan
	// line offer Review disabled with the reason spec 059 not existing yet
	// gives (WL-SPEC-66 §3.3, §3.5) — this instance is WebOpen, so nobody is
	// signed in and every act is offered disabled rather than hidden (§3).
	if !strings.Contains(body, `data-route="rally/add"`) {
		t.Fatalf("progress page has no Rally button:\n%s", body)
	}
	const reviewReason = `data-reason="Review surface (spec 059) not yet built"`
	if got := strings.Count(body, `data-route="review"`); got != 2 {
		t.Fatalf("progress page has %d Review buttons, want 2 (the spec row and the plan line):\n%s", got, body)
	}
	if got := strings.Count(body, reviewReason); got != 2 {
		t.Fatalf("progress page has %d disabled Review reasons, want 2:\n%s", got, body)
	}

	// 5. `lode doc progress --json` decodes to the same derived value.
	out, err := runLodeCLI(t, "doc", "progress", "--project", "prog", "--json")
	if err != nil {
		t.Fatalf("lode doc progress --json: %v\noutput: %s", err, out)
	}
	var fromCLI model.ProjectProgress
	if err := json.Unmarshal([]byte(out), &fromCLI); err != nil {
		t.Fatalf("decode `lode doc progress --json` output %q: %v", out, err)
	}
	if cliSpec := progressSpecByRef(t, fromCLI, spec.Ref); cliSpec.Next.Text != wantNext {
		t.Fatalf("lode doc progress next = %q, want %q", cliSpec.Next.Text, wantNext)
	}

	// 6. The still-open second task picks up a PR, correlated by a signed
	// pull_request delivery, and then a signed merge_group checks_requested
	// (Task 5's fake covers the merge route itself; e2e never calls GitHub).
	// Both webhook deliveries are answered only once their apply has
	// committed, but the row fragment is a separate read against the same
	// shared Postgres instance, so its visibility of that write is polled
	// against a bounded deadline rather than trusted on the first read
	// (see readProgressFrame's comment on the commit horizon above).
	openTask := accepted.Tasks[1]
	const (
		queuePR      = 7
		queueHeadSHA = "ccc3330000000000000000000000000000000000"
		queueBaseSHA = "ddd4440000000000000000000000000000000000"
	)
	deliverGitHub(t, srv.URL, "pull_request", "e2e-progress-pr-1", map[string]any{
		"action":     "opened",
		"repository": map[string]any{"full_name": repo},
		"pull_request": map[string]any{
			"number":     queuePR,
			"title":      "Queue me",
			"state":      "open",
			"merged":     false,
			"body":       "",
			"html_url":   fmt.Sprintf("https://github.com/%s/pull/%d", repo, queuePR),
			"created_at": "2026-07-19T10:00:00Z",
			"updated_at": "2026-07-19T10:00:00Z",
			"head":       map[string]any{"ref": openTask.ID + "-queue-it", "sha": queueHeadSHA},
			"user":       map[string]any{"login": "planner"},
		},
	})
	mergeGroupHeadRef := fmt.Sprintf("refs/heads/gh-readonly-queue/main/pr-%d-%s", queuePR, queueBaseSHA)
	deliverGitHub(t, srv.URL, "merge_group", "e2e-progress-mg-1", map[string]any{
		"action":     "checks_requested",
		"repository": map[string]any{"full_name": repo},
		"merge_group": map[string]any{
			"head_sha": queueHeadSHA,
			"head_ref": mergeGroupHeadRef,
			"base_sha": queueBaseSHA,
			"base_ref": "refs/heads/main",
		},
	})

	fragURL := fmt.Sprintf("%s/projects/prog/progress/spec/%d", srv.URL, spec.ID)
	wantQueuedTip := fmt.Sprintf(`data-tip="%s · %s · queued for merge"`, openTask.ID, openTask.Title)
	pollProgressFragment(t, fragURL, "PR queued for merge",
		func(body string) bool { return strings.Contains(body, wantQueuedTip) })

	deliverGitHub(t, srv.URL, "merge_group", "e2e-progress-mg-2", map[string]any{
		"action":     "destroyed",
		"reason":     "merged",
		"repository": map[string]any{"full_name": repo},
		"merge_group": map[string]any{
			"head_sha": queueHeadSHA,
			"head_ref": mergeGroupHeadRef,
			"base_sha": queueBaseSHA,
			"base_ref": "refs/heads/main",
		},
	})
	pollProgressFragment(t, fragURL, "PR left the merge queue",
		func(body string) bool { return !strings.Contains(body, "queued for merge") })
}

// pollProgressFragment polls the project progress row fragment at url until
// ok reports true on its body, or fails the test after 20s — the same bound
// as store.AwaitCommitHorizon, since the fragment is read over a separate
// connection from the one the webhook committed on, and a busy shared
// Postgres instance (CI's e2e Postgres is one) can hold that back a while.
// Never trust the first read: the caller states the condition it actually
// needs, not "whatever is there right now".
func pollProgressFragment(t *testing.T, url, why string, ok func(body string) bool) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var status int
	var body string
	for {
		status, body = getPage(t, url)
		if status != 200 {
			t.Fatalf("GET %s status = %d", url, status)
		}
		if ok(body) {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("progress row fragment: %s never true after 20s:\n%s", why, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readProgressFrame scans an open progress-events response body, line by
// line, until a "data: " line decodes to a frame naming both doc among its
// Specs and task — tolerating any number of ":" heartbeat comments and
// blank lines ahead of it, and skipping the document's own doc.created/
// wl:DocumentAccepted frames, which also name doc (WL-768). It fails the
// test if the stream ends or its context expires first, rather than
// hanging or trusting the first line written.
func readProgressFrame(t *testing.T, body io.Reader, doc int64, task string) model.ProgressEventFrame {
	t.Helper()
	sc := bufio.NewScanner(body)
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var frame model.ProgressEventFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("decode progress frame %q: %v", line, err)
		}
		if frame.Task == task && slices.Contains(frame.Specs, doc) {
			return frame
		}
	}
	t.Fatalf("progress stream ended without a frame naming doc %d task %s: %v", doc, task, sc.Err())
	return model.ProgressEventFrame{}
}

// progressSpecByRef returns the one spec with the given reference, whichever
// group it landed in, and fails the test if the reading has no such spec.
func progressSpecByRef(t *testing.T, p model.ProjectProgress, ref string) model.ProgressSpec {
	t.Helper()
	for _, g := range p.Groups {
		for _, s := range g.Specs {
			if s.Ref == ref {
				return s
			}
		}
	}
	t.Fatalf("progress reading has no spec %s: %+v", ref, p.Groups)
	return model.ProgressSpec{}
}
