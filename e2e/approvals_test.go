//go:build e2e

// approvals_test.go proves the approval lifecycle (spec 029 §7) over public
// surfaces only: signed GitHub deliveries open and resolve the requirement,
// the Reviews page renders it, and the decide route refuses a request that
// carries no browser session. Nothing here writes to the store directly, and
// the approval id comes from the rendered form, not from a query.
package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// decideAction finds the decide form's action on the Reviews page. Reading it
// out of the HTML is the assertion: the rendered control and the registered
// route have to agree.
var decideAction = regexp.MustCompile(`action="(/approvals/\d+/decide)"`)

func TestApprovalLifecycle(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken:      bootstrapToken,
		GitHubWebhookSecret: githubSecret,
		// No login provider is configured, so the cockpit serves pages only
		// because the deployment opted in. The decide route is gated on a
		// session regardless — that is what step 2 below proves.
		WebOpen: true,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "gov", Name: "Governed", Key: "GOV",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.AddRepo(ctx, "gov", repo, ""); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "agent-a", Kind: "agent", DisplayName: "Agent A",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-a", "e2e approvals", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	agent := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	// The claim mints the branch the PR must carry: without that correlation
	// the ingest opens no approval at all.
	task, _, err := agent.CreateTask(ctx, model.CreateTaskInput{
		Project: "gov", Title: "Govern the merge", Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	claim, _, err := agent.ClaimTask(ctx, task.ID, "e2e", 0)
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}

	const prTitle = "Govern the merge"
	now := func() string { return time.Now().UTC().Format(time.RFC3339Nano) }

	// 1. A task-correlated PR opens → the requirement is a visible awaiting row.
	deliverGitHub(t, srv.URL, "pull_request", "e2e-approval-pr-opened", map[string]any{
		"action":     "opened",
		"repository": map[string]any{"full_name": repo},
		"pull_request": map[string]any{
			"number":     77,
			"title":      prTitle,
			"state":      "open",
			"html_url":   "https://github.com/" + repo + "/pull/77",
			"created_at": now(),
			"user":       map[string]any{"login": "author-ann"},
			"head":       map[string]any{"ref": claim.Branch, "sha": headSHA},
		},
	})

	code, body := getPage(t, srv.URL+"/reviews")
	if code != http.StatusOK {
		t.Fatalf("GET /reviews: status = %d, want 200", code)
	}
	if !strings.Contains(body, prTitle) {
		t.Fatalf("reviews page does not list the awaiting PR %q:\n%s", prTitle, body)
	}

	m := decideAction.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("reviews page has no decide form action:\n%s", body)
	}
	action := m[1]

	// 2. The same form, submitted without a session, is refused — and the row
	// is still awaiting afterwards.
	code, _ = postForm(t, srv.URL, action, url.Values{"decision": {"approve"}})
	if code != http.StatusForbidden {
		t.Fatalf("POST %s without a session: status = %d, want 403", action, code)
	}
	code, body = getPage(t, srv.URL+"/reviews")
	if code != http.StatusOK {
		t.Fatalf("GET /reviews after the refused decision: status = %d, want 200", code)
	}
	if !strings.Contains(body, prTitle) {
		t.Fatalf("refused decision removed the awaiting row:\n%s", body)
	}

	// 3. An approving review resolves the requirement, so the queue empties.
	deliverGitHub(t, srv.URL, "pull_request_review", "e2e-approval-review", map[string]any{
		"action":       "submitted",
		"repository":   map[string]any{"full_name": repo},
		"pull_request": map[string]any{"number": 77},
		"review": map[string]any{
			"user":         map[string]any{"login": "reviewer-bob"},
			"state":        "approved",
			"submitted_at": now(),
		},
	})

	code, body = getPage(t, srv.URL+"/reviews")
	if code != http.StatusOK {
		t.Fatalf("GET /reviews after the review: status = %d, want 200", code)
	}
	if strings.Contains(body, prTitle) {
		t.Fatalf("reviews page still lists the resolved approval:\n%s", body)
	}
	if !strings.Contains(body, "No approvals are waiting.") {
		t.Fatalf("reviews page does not report an empty queue:\n%s", body)
	}
}
