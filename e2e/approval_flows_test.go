//go:build e2e

// approval_flows_test.go proves a project's review flow lifecycle (029 §7.2)
// over public surfaces only: applying a named flow to a project, the
// backfill it runs against a deliverable the project already holds, filing
// an ad-hoc requirement by hand, and the decide gate holding for both kinds
// on the real wire. The gate itself — a session-authenticated decide
// succeeding — is already proven in internal/api against the fake OIDC
// issuer; this only reconfirms the refusal holds here too, on the actual e2e
// stack (LODE_WEB_OPEN=true, no issuer configured).
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// postJSON posts a bearer-authenticated JSON body and decodes a 2xx response
// into v (skipped when v is nil). The one write this file needs that has no
// cli.Client wrapper — creating a deliverable — goes through this instead of
// growing one just for this test.
func postJSON(t *testing.T, targetURL, token string, payload, v any) int {
	t.Helper()
	body := mustJSON(t, payload)
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request for %s: %v", targetURL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", targetURL, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response from %s: %v", targetURL, err)
	}
	if v != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := json.Unmarshal(respBody, v); err != nil {
			t.Fatalf("decode response from %s: %v (body %s)", targetURL, err, respBody)
		}
	}
	return resp.StatusCode
}

func TestApprovalFlow(t *testing.T) {
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

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "story", Name: "Story Project", Key: "STORY",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// 1. Apply the "story" flow: it stamps the project at rev 1.
	applied, _, err := admin.ApplyApprovalFlow(ctx, "story", "story", nil)
	if err != nil {
		t.Fatalf("apply approval flow: %v", err)
	}
	if applied.Project.ApprovalFlowName != "story" || applied.Project.ApprovalFlowRev != "1" {
		t.Fatalf("applied flow = %+v, want name story rev 1", applied.Project)
	}

	// 2. Declaring a deliverable the flow names by target ("Methodology")
	// backfills both lanes the flow demands of it. Neither carries a
	// revision — a deliverable has none — so the Reviews page shows both
	// rows without a decide form.
	var deliverable model.Deliverable
	status := postJSON(t, srv.URL+"/api/v1/projects/story/deliverables", bootstrapToken,
		model.CreateDeliverableInput{Name: "Methodology"}, &deliverable)
	if status != http.StatusCreated {
		t.Fatalf("create deliverable: status = %d, want 201", status)
	}

	code, body := getPage(t, srv.URL+"/reviews")
	if code != http.StatusOK {
		t.Fatalf("GET /reviews: status = %d, want 200", code)
	}
	for _, lane := range []string{"methodology/science-lead", "methodology/domain-expert"} {
		if !strings.Contains(body, lane) {
			t.Fatalf("reviews page missing lane %q:\n%s", lane, body)
		}
	}
	if n := strings.Count(body, "No revision designated for review yet."); n != 2 {
		t.Fatalf("reviews page has %d undecidable rows, want 2:\n%s", n, body)
	}
	if strings.Contains(body, `class="decide"`) {
		t.Fatalf("reviews page already has a decide form before any row names a revision:\n%s", body)
	}

	// 3. File an ad-hoc requirement on a task, naming an explicit revision:
	// unlike the deliverable rows above, this makes the row decidable.
	task, _, err := admin.CreateTask(ctx, model.CreateTaskInput{
		Project: "story", Title: "Draft the methodology", Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	reqIn := model.RequireApprovalInput{
		EntityKind: "task", EntityID: task.ID, Revision: "1", Lane: "sign-off",
	}
	approval, _, err := admin.RequireApproval(ctx, reqIn)
	if err != nil {
		t.Fatalf("require approval: %v", err)
	}

	code, body = getPage(t, srv.URL+"/reviews")
	if code != http.StatusOK {
		t.Fatalf("GET /reviews after the ad-hoc requirement: status = %d, want 200", code)
	}
	if !strings.Contains(body, task.Title) {
		t.Fatalf("reviews page missing the ad-hoc row for %q:\n%s", task.Title, body)
	}
	m := decideAction.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("reviews page has no decide form for the ad-hoc row:\n%s", body)
	}
	action := m[1]

	// 4. Part 1's gate holds for the new kinds too: a decide with no session
	// is refused on the real wire, and the row stays awaiting.
	code, _ = postForm(t, srv.URL, action, url.Values{"decision": {"approve"}})
	if code != http.StatusForbidden {
		t.Fatalf("POST %s without a session: status = %d, want 403", action, code)
	}
	code, body = getPage(t, srv.URL+"/reviews")
	if code != http.StatusOK {
		t.Fatalf("GET /reviews after the refused decide: status = %d, want 200", code)
	}
	if !strings.Contains(body, task.Title) {
		t.Fatalf("the refused decide removed the ad-hoc row:\n%s", body)
	}

	// 5. Idempotence over the wire: re-applying the flow materializes
	// nothing new, and re-filing the same ad-hoc requirement returns the
	// same row rather than a duplicate.
	reapplied, _, err := admin.ApplyApprovalFlow(ctx, "story", "story", nil)
	if err != nil {
		t.Fatalf("re-apply approval flow: %v", err)
	}
	if reapplied.Materialized != 0 {
		t.Fatalf("re-apply materialized = %d, want 0", reapplied.Materialized)
	}
	refiled, _, err := admin.RequireApproval(ctx, reqIn)
	if err != nil {
		t.Fatalf("re-file the ad-hoc requirement: %v", err)
	}
	if refiled.ID != approval.ID {
		t.Fatalf("re-filed approval id = %d, want the same row %d", refiled.ID, approval.ID)
	}
}
