//go:build e2e

// Package e2e drives the full work-tracker stack end-to-end through its
// public surfaces only: the HTTP API (via cli.Client and raw requests), the
// GitHub and Flux webhook endpoints (signed like real deliveries), and the
// read-only web pages. No direct store writes — if a step fails here, the
// plan→production chain is broken for real users too.
package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/work-tracker/internal/api"
	"github.com/sunstoneinstitute/work-tracker/internal/cli"
	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

const (
	// bootstrapToken carries the documented "wt_" prefix (README): the auth
	// layer treats a non-wt_ credential as a token hash, not a plaintext.
	bootstrapToken = "wt_e2e-bootstrap-token"
	githubSecret   = "e2e-github-secret"
	fluxSecret     = "e2e-flux-secret"

	repo     = "sunstoneinstitute/demo"
	headSHA  = "aaa1110000000000000000000000000000000000"
	mergeSHA = "bbb2220000000000000000000000000000000000"
)

// sign computes the "sha256=<hex>" HMAC both webhook endpoints expect.
// (Re-implemented here: the hooks test helpers are package-internal.)
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// postSigned delivers a signed webhook body and asserts the endpoint answers
// 200 {"status": "ok"}.
func postSigned(t *testing.T, url string, headers map[string]string, body []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build webhook request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("deliver webhook to %s: %v", url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook %s: status = %d, body %s", url, resp.StatusCode, respBody)
	}
	var m map[string]string
	if err := json.Unmarshal(respBody, &m); err != nil || m["status"] != "ok" {
		t.Fatalf("webhook %s: body = %s, want status ok (err %v)", url, respBody, err)
	}
}

// deliverGitHub signs and posts one GitHub webhook delivery.
func deliverGitHub(t *testing.T, baseURL, event, deliveryID string, payload any) {
	t.Helper()
	body := mustJSON(t, payload)
	postSigned(t, baseURL+"/hooks/github", map[string]string{
		"Content-Type":        "application/json",
		"X-Hub-Signature-256": sign(githubSecret, body),
		"X-GitHub-Event":      event,
		"X-GitHub-Delivery":   deliveryID,
	}, body)
}

// deliverFlux signs and posts one Flux notification-controller event.
func deliverFlux(t *testing.T, baseURL string, payload any) {
	t.Helper()
	body := mustJSON(t, payload)
	postSigned(t, baseURL+"/hooks/flux", map[string]string{
		"Content-Type": "application/json",
		"X-Signature":  sign(fluxSecret, body),
	}, body)
}

// getPage fetches a web page and returns its status code and body.
func getPage(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, string(body)
}

func TestFullChain(t *testing.T) {
	ctx := context.Background()

	// 1. Real stack: store on a temp dir, full server, real HTTP listener.
	st, err := store.Open(filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	handler, err := api.NewServer(st, api.Config{
		BootstrapToken:      bootstrapToken,
		GitHubWebhookSecret: githubSecret,
		FluxWebhookSecret:   fluxSecret,
		ClusterEnvMap:       map[string]string{"testcluster": "prod"},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// 2. Bootstrap admin sets up project, repo, and an agent actor + token.
	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, cli.CreateProjectInput{
		ID: "demo", Name: "Demo", DeployGated: false,
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := admin.AddRepo(ctx, "demo", repo); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, cli.CreateActorInput{
		ID: "agent-1", Kind: "agent", DisplayName: "Agent One",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-1", "e2e smoke", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if tok.Token == "" {
		t.Fatal("create token returned empty plaintext token")
	}
	agent := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	// 3. Task flow: create + claim as the agent.
	task, _, err := agent.CreateTask(ctx, cli.CreateTaskInput{
		Project: "demo", Title: "Add login page", Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.State != "ready" {
		t.Fatalf("created task state = %q, want ready", task.State)
	}
	claim, _, err := agent.ClaimTask(ctx, task.ID, "e2e", 0)
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	wantBranch := "wt/" + task.ID + "-add-login-page"
	if claim.Branch != wantBranch {
		t.Fatalf("claim branch = %q, want %q", claim.Branch, wantBranch)
	}

	// 4. GitHub webhooks, in real delivery order. Payload timestamps are
	// taken as each delivery is built, so the timeline ends up ascending.
	now := func() string { return time.Now().UTC().Format(time.RFC3339) }
	prOpenedAt := now()

	// 4a. pull_request.opened on the claim branch → task moves to in_review.
	deliverGitHub(t, srv.URL, "pull_request", "e2e-pr-opened", map[string]any{
		"action":     "opened",
		"repository": map[string]any{"full_name": repo},
		"pull_request": map[string]any{
			"number":     42,
			"title":      "Add login page",
			"state":      "open",
			"html_url":   "https://github.com/" + repo + "/pull/42",
			"created_at": prOpenedAt,
			"head":       map[string]any{"ref": claim.Branch, "sha": headSHA},
		},
	})
	detail, _, err := agent.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task after PR opened: %v", err)
	}
	if detail.State != "in_review" {
		t.Fatalf("task state after PR opened = %q, want in_review", detail.State)
	}

	// 4b. workflow_run.completed (success) on the PR head SHA → ci_run row.
	deliverGitHub(t, srv.URL, "workflow_run", "e2e-wf-1", map[string]any{
		"action":     "completed",
		"repository": map[string]any{"full_name": repo},
		"workflow_run": map[string]any{
			"name":           "CI",
			"head_sha":       headSHA,
			"status":         "completed",
			"conclusion":     "success",
			"html_url":       "https://github.com/" + repo + "/actions/runs/1",
			"run_started_at": now(),
			"updated_at":     now(),
		},
	})
	tl, _, err := agent.Timeline(ctx, task.ID)
	if err != nil {
		t.Fatalf("timeline after workflow_run: %v", err)
	}
	ci := findEntry(tl.Timeline, "ci")
	if ci == nil {
		t.Fatalf("timeline after workflow_run has no ci entry: %v", entryTypes(tl.Timeline))
	}
	if ci["workflow"] != "CI" || ci["status"] != "completed" || ci["conclusion"] != "success" {
		t.Fatalf("ci entry = %v, want workflow CI completed/success", ci)
	}

	// 4c. pull_request_review.submitted (approved).
	deliverGitHub(t, srv.URL, "pull_request_review", "e2e-review-1", map[string]any{
		"action":       "submitted",
		"repository":   map[string]any{"full_name": repo},
		"pull_request": map[string]any{"number": 42},
		"review": map[string]any{
			"user":         map[string]any{"login": "reviewer-bob"},
			"state":        "approved",
			"submitted_at": now(),
		},
	})

	// 4d. pull_request.closed merged → non-gated project: task done, lease
	// released.
	deliverGitHub(t, srv.URL, "pull_request", "e2e-pr-merged", map[string]any{
		"action":     "closed",
		"repository": map[string]any{"full_name": repo},
		"pull_request": map[string]any{
			"number":           42,
			"title":            "Add login page",
			"state":            "closed",
			"merged":           true,
			"merge_commit_sha": mergeSHA,
			"html_url":         "https://github.com/" + repo + "/pull/42",
			"created_at":       prOpenedAt,
			"merged_at":        now(),
			"head":             map[string]any{"ref": claim.Branch, "sha": headSHA},
		},
	})
	detail, _, err = agent.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task after merge: %v", err)
	}
	if detail.State != "done" {
		t.Fatalf("task state after merge = %q, want done", detail.State)
	}
	if detail.Lease != nil {
		t.Fatalf("task lease after merge = %+v, want released (nil)", detail.Lease)
	}

	// 4e. release.published from the merge commit → git_tag artifact.
	deliverGitHub(t, srv.URL, "release", "e2e-release-1", map[string]any{
		"action":     "published",
		"repository": map[string]any{"full_name": repo},
		"release": map[string]any{
			"tag_name":         "v1.0.0",
			"target_commitish": mergeSHA,
			"published_at":     now(),
		},
	})

	// 5. Flux webhook: reconciliation success on the release revision in
	// cluster "testcluster" → deployment "deployed" in env "prod", linked to
	// the artifact.
	deliverFlux(t, srv.URL, map[string]any{
		"involvedObject": map[string]any{
			"kind": "Kustomization", "namespace": "flux-system", "name": "demo",
		},
		"severity":  "info",
		"timestamp": now(),
		"message":   "Applied revision: main@sha1:" + mergeSHA,
		"reason":    "ReconciliationSucceeded",
		"metadata": map[string]any{
			"revision": "main@sha1:" + mergeSHA,
			"cluster":  "testcluster",
		},
	})

	// 6. Watcher path: one crashloop runtime event with a dedupe key. Its
	// image matches no artifact, so it stays unlinked (and off the task
	// timeline) — it must surface on the board instead.
	postRuntimeEvent(t, srv.URL, tok.Token, map[string]any{
		"cluster":     "testcluster",
		"kind":        "crashloop",
		"workload":    "default/demo",
		"image":       "ghcr.io/sunstoneinstitute/unrelated:sha-zzz",
		"message":     "CrashLoopBackOff",
		"occurred_at": now(),
		"dedupe_key":  "e2e-crashloop-1",
	})

	// 7. Final assertions.
	assertTimeline(t, ctx, agent, task.ID)
	assertBoard(t, ctx, agent)
	assertWebPages(t, srv.URL, task.ID)

	// 8. Inbox flow: issues.opened webhook → promote to a ready task.
	deliverGitHub(t, srv.URL, "issues", "e2e-issue-1", map[string]any{
		"action":     "opened",
		"repository": map[string]any{"full_name": repo},
		"issue": map[string]any{
			"number":   7,
			"title":    "Crash on load",
			"state":    "open",
			"html_url": "https://github.com/" + repo + "/issues/7",
		},
	})
	issues, _, err := agent.ListIssues(ctx, "new")
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	if len(issues.Issues) != 1 || issues.Issues[0].Number != 7 {
		t.Fatalf("inbox = %+v, want the one opened issue #7", issues.Issues)
	}
	promoted, _, err := agent.PromoteIssue(ctx, cli.PromoteInput{
		Repo:              repo,
		Number:            7,
		Priority:          "medium",
		Kind:              "bug",
		AppliesToVersions: []string{"v1.0.0"},
	})
	if err != nil {
		t.Fatalf("promote issue: %v", err)
	}
	if promoted.State != "ready" || promoted.Project != "demo" || promoted.Title != "Crash on load" {
		t.Fatalf("promoted task = %+v, want ready bug in demo titled from the issue", promoted)
	}
	pd, _, err := agent.GetTask(ctx, promoted.ID)
	if err != nil {
		t.Fatalf("get promoted task: %v", err)
	}
	if pd.State != "ready" {
		t.Fatalf("promoted task state = %q, want ready", pd.State)
	}
}

// postRuntimeEvent posts to /api/v1/runtime-events with a bearer token and
// asserts 201 {"status": "ok"}. cli.Client has no runtime-event method (it is
// the watcher's endpoint), so this uses the raw HTTP surface.
func postRuntimeEvent(t *testing.T, baseURL, token string, payload any) {
	t.Helper()
	body := mustJSON(t, payload)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/runtime-events", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build runtime-event request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post runtime event: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("runtime event: status = %d, body %s, want 201", resp.StatusCode, respBody)
	}
	var m map[string]any
	if err := json.Unmarshal(respBody, &m); err != nil || m["status"] != "ok" {
		t.Fatalf("runtime event body = %s, want status ok (err %v)", respBody, err)
	}
}

func entryTypes(timeline []map[string]any) []string {
	var types []string
	for _, e := range timeline {
		typ, _ := e["type"].(string)
		types = append(types, typ)
	}
	return types
}

// findEntry returns the first timeline entry of the given type, or nil.
func findEntry(timeline []map[string]any, typ string) map[string]any {
	for _, e := range timeline {
		if e["type"] == typ {
			return e
		}
	}
	return nil
}

// assertTimeline checks the task timeline is ascending by time and contains,
// in order, the whole chain: state changes, the PR, its CI run, the review,
// the release artifact, and the deployment. The crashloop runtime event is
// unlinked to the artifact, so it must NOT appear here (it is asserted via
// the board's recent_failures instead).
func assertTimeline(t *testing.T, ctx context.Context, agent *cli.Client, taskID string) {
	t.Helper()
	tl, _, err := agent.Timeline(ctx, taskID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	types := entryTypes(tl.Timeline)

	// Ascending by "at".
	var prev time.Time
	for i, e := range tl.Timeline {
		atStr, _ := e["at"].(string)
		ts, err := time.Parse(time.RFC3339, atStr)
		if err != nil {
			t.Fatalf("timeline entry %d: bad at %q: %v", i, atStr, err)
		}
		if ts.Before(prev) {
			t.Fatalf("timeline not ascending at entry %d (%s): %v < %v (types %v)",
				i, types[i], ts, prev, types)
		}
		prev = ts
	}

	// The chain types appear, in ascending order of first occurrence.
	firstIdx := func(typ string) int {
		for i, x := range types {
			if x == typ {
				return i
			}
		}
		t.Fatalf("timeline missing %q entry: %v", typ, types)
		return -1
	}
	order := []string{"state", "pr", "ci", "review", "artifact", "deployment"}
	last := -1
	for _, typ := range order {
		idx := firstIdx(typ)
		if idx <= last {
			t.Fatalf("timeline type order wrong: %q at %d not after previous chain entry at %d (types %v)",
				typ, idx, last, types)
		}
		last = idx
	}
	if types[0] != "state" {
		t.Fatalf("timeline starts with %q, want state (types %v)", types[0], types)
	}
	if e := findEntry(tl.Timeline, "runtime"); e != nil {
		t.Fatalf("timeline contains a runtime entry %v; the unlinked crashloop must not appear", e)
	}

	// Spot-check entry contents.
	pr := findEntry(tl.Timeline, "pr")
	if pr["repo"] != repo || pr["number"] != float64(42) || pr["state"] != "merged" {
		t.Fatalf("pr entry = %v, want merged %s#42", pr, repo)
	}
	review := findEntry(tl.Timeline, "review")
	if review["reviewer"] != "reviewer-bob" || review["state"] != "approved" {
		t.Fatalf("review entry = %v, want reviewer-bob approved", review)
	}
	artifact := findEntry(tl.Timeline, "artifact")
	if artifact["kind"] != "git_tag" || artifact["name"] != repo || artifact["version"] != "v1.0.0" {
		t.Fatalf("artifact entry = %v, want git_tag %s v1.0.0", artifact, repo)
	}
	deployment := findEntry(tl.Timeline, "deployment")
	if deployment["environment"] != "prod" || deployment["status"] != "deployed" ||
		deployment["target_name"] != "flux-system/demo" {
		t.Fatalf("deployment entry = %v, want prod/deployed on flux-system/demo", deployment)
	}

	// The task's state chain ends at done.
	var lastState map[string]any
	for _, e := range tl.Timeline {
		if e["type"] == "state" {
			lastState = e
		}
	}
	change, _ := lastState["change"].(map[string]any)
	if change == nil || change["new"] != "done" {
		t.Fatalf("last state entry = %v, want change.new done", lastState)
	}
}

// assertBoard checks the board response: the demo project exists, the done
// task sits in no bucket (buckets only show in_progress/in_review/ready/
// blocked), and recent_failures carries the crashloop.
func assertBoard(t *testing.T, ctx context.Context, agent *cli.Client) {
	t.Helper()
	board, _, err := agent.Board(ctx, "")
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	var demo *cli.BoardProject
	for i := range board.Projects {
		if board.Projects[i].ID == "demo" {
			demo = &board.Projects[i]
		}
	}
	if demo == nil {
		t.Fatalf("board has no demo project: %+v", board.Projects)
	}
	if n := len(demo.InProgress) + len(demo.InReview) + len(demo.Ready) + len(demo.Blocked); n != 0 {
		t.Fatalf("board buckets not empty (done task must appear in none): %+v", demo)
	}
	if len(board.RecentFailures) != 1 {
		t.Fatalf("recent_failures = %+v, want exactly the crashloop", board.RecentFailures)
	}
	f := board.RecentFailures[0]
	if f.Kind != "crashloop" || f.Cluster != "testcluster" || f.Message != "CrashLoopBackOff" {
		t.Fatalf("recent failure = %+v, want the posted crashloop", f)
	}
}

// assertWebPages checks the read-only web UI renders: the board page carries
// the project name and the crashloop failure, and the task page loads.
func assertWebPages(t *testing.T, baseURL, taskID string) {
	t.Helper()
	code, body := getPage(t, baseURL+"/")
	if code != http.StatusOK {
		t.Fatalf("GET /: status = %d, want 200", code)
	}
	if !strings.Contains(body, "Demo") {
		t.Fatalf("board page does not mention project Demo:\n%s", body)
	}
	if !strings.Contains(body, "CrashLoopBackOff") {
		t.Fatalf("board page does not show the crashloop failure:\n%s", body)
	}
	code, body = getPage(t, fmt.Sprintf("%s/tasks/%s", baseURL, taskID))
	if code != http.StatusOK {
		t.Fatalf("GET /tasks/%s: status = %d, want 200 (body %s)", taskID, code, body)
	}
}
