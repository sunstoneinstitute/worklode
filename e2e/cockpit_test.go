//go:build e2e

// cockpit_test.go proves the Part 1 project cockpit (docs/specs/032-project-cockpit.md)
// end to end, through public surfaces only: the bearer-token API, a signed
// GitHub webhook delivery, and the read-only web UI. It never calls a
// store writer directly — every fact in the scenario (project, repo mapping,
// actors, tokens, task, assignment, claim, in-review transition, blocker
// edge) is produced the same way a real caller would produce it.
package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// cockpitRepo is the repo mapped to the cockpit test's project — distinct
// from smoke_test.go's repo constant so the two scenarios never share fixture
// identity even though they run against independent servers/databases.
const cockpitRepo = "acme/app"

// --- cockpit JSON wire shape ------------------------------------------------
//
// internal/cli has no typed client for GET /projects/{id}/cockpit (it is
// consumed by the web UI in-process, not by the CLI), so this test decodes
// the same JSON shape cockpit.go's cockpitProjection emits directly.

type cockpitActorJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cockpitEvidenceJSON struct {
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

type cockpitWorkItemJSON struct {
	ID             string               `json:"id"`
	Title          string               `json:"title"`
	Priority       string               `json:"priority"`
	State          string               `json:"state"`
	Blocked        bool                 `json:"blocked"`
	URL            string               `json:"url"`
	Owner          *cockpitActorJSON    `json:"owner"`
	Delegate       *cockpitActorJSON    `json:"delegate"`
	StatusEvidence *cockpitEvidenceJSON `json:"status_evidence"`
}

type cockpitWorkJSON struct {
	InProgress []cockpitWorkItemJSON `json:"in_progress"`
	InReview   []cockpitWorkItemJSON `json:"in_review"`
	Ready      []cockpitWorkItemJSON `json:"ready"`
	Blocked    []cockpitWorkItemJSON `json:"blocked"`
}

type cockpitSecondaryConcernJSON struct {
	Kind     string              `json:"kind"`
	Title    string              `json:"title"`
	URL      string              `json:"url"`
	Evidence cockpitEvidenceJSON `json:"evidence"`
}

type cockpitRepositoryJSON struct {
	Repo           string              `json:"repo"`
	DoneState      string              `json:"done_state"`
	StatusEvidence cockpitEvidenceJSON `json:"status_evidence"`
}

type cockpitProjectionJSON struct {
	CanonicalURL string `json:"canonical_url"`
	Project      struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Key  string `json:"key"`
	} `json:"project"`
	Mode struct {
		Name  string              `json:"name"`
		Basis cockpitEvidenceJSON `json:"basis"`
	} `json:"mode"`
	PinnedFocus       any                           `json:"pinned_focus"`
	RankingFocus      []string                      `json:"ranking_focus"`
	NextDecision      any                           `json:"next_decision"`
	Work              cockpitWorkJSON               `json:"work"`
	SecondaryConcerns []cockpitSecondaryConcernJSON `json:"secondary_concerns"`
	Repositories      []cockpitRepositoryJSON       `json:"repositories"`
	Cost              json.RawMessage               `json:"cost"`
}

// findWorkItem returns the item with id in items, or fails the test.
func findWorkItem(t *testing.T, items []cockpitWorkItemJSON, id string) cockpitWorkItemJSON {
	t.Helper()
	for _, it := range items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("work item %s not found in %+v", id, items)
	return cockpitWorkItemJSON{}
}

// TestProjectCockpitPublicSurface is Part 1's durable acceptance test: it
// builds a project's whole cockpit-relevant state through public writes only
// (HTTP API calls plus one signed GitHub webhook delivery — never a direct
// store write), then asserts the JSON cockpit contract and the rendered
// Overview/placeholder/asset surfaces the web UI serves from it.
func TestProjectCockpitPublicSurface(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken:      bootstrapToken,
		GitHubWebhookSecret: githubSecret,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})

	// 1. Project "proj", key "WL", mapped to acme/app.
	if _, _, err := admin.CreateProject(ctx, cli.CreateProjectInput{
		ID: "proj", Name: "Proj", Key: "WL",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.AddRepo(ctx, "proj", cockpitRepo, ""); err != nil {
		t.Fatalf("add repo: %v", err)
	}

	// 2. A human actor (Dana) and an agent actor (Agent One), each with their
	// own bearer token.
	if _, _, err := admin.CreateActor(ctx, cli.CreateActorInput{
		ID: "dana", Kind: "human", DisplayName: "Dana",
	}); err != nil {
		t.Fatalf("create actor dana: %v", err)
	}
	danaTok, _, err := admin.CreateToken(ctx, "dana", "e2e cockpit", nil)
	if err != nil {
		t.Fatalf("create token for dana: %v", err)
	}

	if _, _, err := admin.CreateActor(ctx, cli.CreateActorInput{
		ID: "agent-one", Kind: "agent", DisplayName: "Agent One",
	}); err != nil {
		t.Fatalf("create actor agent-one: %v", err)
	}
	agentTok, _, err := admin.CreateToken(ctx, "agent-one", "e2e cockpit", nil)
	if err != nil {
		t.Fatalf("create token for agent-one: %v", err)
	}
	agentOne := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: agentTok.Token})

	// 3. One task, assigned to Dana, claimed by Agent One, moved to review by
	// a signed pull_request.opened webhook delivery — never a direct store
	// write.
	task, _, err := admin.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Ship the cockpit", Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, _, err := admin.AssignTask(ctx, task.ID, "dana"); err != nil {
		t.Fatalf("assign task to dana: %v", err)
	}
	claim, _, err := agentOne.ClaimTask(ctx, task.ID, "host:/wt-agent-one", 0)
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}

	deliverGitHub(t, srv.URL, "pull_request", "e2e-cockpit-pr-opened", map[string]any{
		"action":     "opened",
		"repository": map[string]any{"full_name": cockpitRepo},
		"pull_request": map[string]any{
			"number":     1,
			"title":      "Ship the cockpit",
			"state":      "open",
			"html_url":   "https://github.com/" + cockpitRepo + "/pull/1",
			"created_at": time.Now().UTC().Format(time.RFC3339Nano),
			"head":       map[string]any{"ref": claim.Branch, "sha": "cafe000000000000000000000000000000000c"},
		},
	})

	// 4. A blocker and a dependent task, linked by a "blocks" edge (blocker
	// blocks dependent).
	blocker, _, err := admin.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Blocker task", Priority: "medium", Kind: "chore",
	})
	if err != nil {
		t.Fatalf("create blocker task: %v", err)
	}
	dependent, _, err := admin.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Dependent task", Priority: "medium", Kind: "chore",
	})
	if err != nil {
		t.Fatalf("create dependent task: %v", err)
	}
	if _, err := admin.Block(ctx, dependent.ID, blocker.ID); err != nil {
		t.Fatalf("block dependent on blocker: %v", err)
	}

	// --- Step 2: the cockpit API contract, fetched as Dana ------------------

	got := getCockpit(t, srv.URL, danaTok.Token, "proj")

	if got.Mode.Name != "operations" {
		t.Fatalf("mode = %q, want operations", got.Mode.Name)
	}
	if got.CanonicalURL != "/projects/proj" {
		t.Fatalf("canonical = %q, want /projects/proj", got.CanonicalURL)
	}
	if got.PinnedFocus != nil || got.NextDecision != nil {
		t.Fatalf("pinned_focus = %#v, next_decision = %#v, want both nil (no governed object in Part 1)",
			got.PinnedFocus, got.NextDecision)
	}
	if got.RankingFocus == nil {
		t.Fatalf("ranking_focus decoded nil, want [] (empty JSON array, never null)")
	}
	if got.Work.InProgress == nil || len(got.Work.InProgress) != 0 {
		t.Fatalf("work.in_progress = %#v, want an empty (non-nil) list", got.Work.InProgress)
	}

	item := findWorkItem(t, got.Work.InReview, task.ID)
	if item.Owner == nil || item.Owner.ID != "dana" {
		t.Fatalf("owner = %#v, want dana", item.Owner)
	}
	if item.Delegate == nil || item.Delegate.ID != "agent-one" {
		t.Fatalf("delegate = %#v, want agent-one", item.Delegate)
	}
	if item.StatusEvidence == nil || item.StatusEvidence.Category != "observed" {
		t.Fatalf("status_evidence = %#v, want category observed", item.StatusEvidence)
	}

	blockedItem := findWorkItem(t, got.Work.Blocked, dependent.ID)
	if !blockedItem.Blocked {
		t.Fatalf("dependent task blocked = %v, want true", blockedItem.Blocked)
	}
	foundConcern := false
	for _, c := range got.SecondaryConcerns {
		if c.Kind == "blocker" && c.Title == "Blocker task" && c.URL == "/tasks/"+blocker.ID {
			foundConcern = true
		}
	}
	if !foundConcern {
		t.Fatalf("secondary_concerns = %+v, want a blocker entry for %s", got.SecondaryConcerns, blocker.ID)
	}
	if got.Repositories == nil || len(got.Repositories) != 1 || got.Repositories[0].Repo != cockpitRepo {
		t.Fatalf("repositories = %+v, want exactly [%s]", got.Repositories, cockpitRepo)
	}

	// --- Step 3: the browser-style Overview, canonical across ?variant= -----

	code, body := getPage(t, srv.URL+"/projects/proj")
	if code != http.StatusOK {
		t.Fatalf("GET /projects/proj: status = %d, want 200", code)
	}
	assertOverviewSurface(t, body, blocker.ID, dependent.ID)

	for _, variant := range []string{"A", "B", "C"} {
		code, body := getPage(t, srv.URL+"/projects/proj?variant="+variant)
		if code != http.StatusOK {
			t.Fatalf("GET /projects/proj?variant=%s: status = %d, want 200", variant, code)
		}
		if !strings.Contains(body, "operations") {
			t.Fatalf("variant=%s body does not render Operations mode:\n%s", variant, body)
		}
		if !strings.Contains(body, `<link rel="canonical" href="/projects/proj">`) {
			t.Fatalf("variant=%s body missing the canonical /projects/proj link:\n%s", variant, body)
		}
		assertNoFabrication(t, variant, body)
	}

	// --- Step 4: honest destinations and embedded assets ---------------------

	honestDestinations := map[string]string{
		"/projects/proj/crew":         "spec 029 §6.1",
		"/projects/proj/deliverables": "spec 029 §7",
		"/intake":                     "spec 032 §5",
		"/knowledge":                  "specs 025",
	}
	for path, wantSpec := range honestDestinations {
		code, body := getPage(t, srv.URL+path)
		if code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200", path, code)
		}
		if !strings.Contains(body, wantSpec) {
			t.Fatalf("GET %s: body missing owning-spec sentence %q:\n%s", path, wantSpec, body)
		}
		if strings.Contains(body, "<form") || strings.Contains(body, "<button") {
			t.Fatalf("GET %s unexpectedly renders a form or button:\n%s", path, body)
		}
	}

	for _, path := range []string{
		"/assets/app.css",
		"/assets/fonts/dm-sans-variable.ttf",
		"/assets/fonts/source-serif-4-variable.ttf",
	} {
		code, _ := getPage(t, srv.URL+path)
		if code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200 (no auth redirect)", path, code)
		}
	}
}

// assertOverviewSurface checks the rendered /projects/proj page: two distinct
// navigation landmarks, one main landmark, one decision rail, Dana as owner,
// Agent One as delegate, Observed status evidence, blocker copy, an evidence
// <details> disclosure, and a source link back to the blocking task (the
// "source" the blocked-state evidence traces to).
func assertOverviewSurface(t *testing.T, body, blockerID, dependentID string) {
	t.Helper()

	if got := strings.Count(body, "<nav aria-label="); got != 2 {
		t.Fatalf("nav landmark count = %d, want 2 (Primary + Project):\n%s", got, body)
	}
	if !strings.Contains(body, `<nav aria-label="Primary">`) {
		t.Fatalf("missing the primary global nav landmark:\n%s", body)
	}
	if !strings.Contains(body, `<nav aria-label="Project"`) {
		t.Fatalf("missing the project-local nav landmark:\n%s", body)
	}
	if got := strings.Count(body, `<main id="main-content"`); got != 1 {
		t.Fatalf("main landmark count = %d, want 1:\n%s", got, body)
	}
	if !strings.Contains(body, `<aside aria-label="Next decision">`) {
		t.Fatalf("missing the decision rail:\n%s", body)
	}

	if !strings.Contains(body, "Owned by Dana") {
		t.Fatalf("missing owner copy \"Owned by Dana\":\n%s", body)
	}
	if !strings.Contains(body, "Agent One is the delegate") {
		t.Fatalf("missing delegate copy \"Agent One is the delegate\":\n%s", body)
	}
	if !strings.Contains(body, "Observed:") {
		t.Fatalf("missing Observed status evidence copy:\n%s", body)
	}
	if !strings.Contains(body, "Blocks "+dependentID+" (blocker state") {
		t.Fatalf("missing blocker copy \"Blocks %s (blocker state ...)\":\n%s", dependentID, body)
	}
	if !strings.Contains(body, "<details>") || !strings.Contains(body, "<summary>Evidence</summary>") {
		t.Fatalf("missing the evidence <details> disclosure:\n%s", body)
	}
	if !strings.Contains(body, `<a href="/tasks/`+blockerID+`">`) {
		t.Fatalf("missing the source link back to the blocking task /tasks/%s:\n%s", blockerID, body)
	}
}

// assertNoFabrication checks a rendered Overview variant carries none of the
// forbidden, non-existent-in-Part-1 concepts: a bare percentage, a
// project-health readout, or a prototype form/button control.
func assertNoFabrication(t *testing.T, variant, body string) {
	t.Helper()
	for _, forbidden := range []string{"%", "project health", "<form", "<button"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("variant=%s unexpectedly renders %q:\n%s", variant, forbidden, body)
		}
	}
}

// getCockpit fetches GET /api/v1/projects/{id}/cockpit with a bearer token
// and decodes the cockpit projection.
func getCockpit(t *testing.T, baseURL, token, projectID string) cockpitProjectionJSON {
	t.Helper()
	code, body := getAuthed(t, baseURL+"/api/v1/projects/"+projectID+"/cockpit", token)
	if code != http.StatusOK {
		t.Fatalf("GET cockpit: status = %d, body %s", code, body)
	}
	var got cockpitProjectionJSON
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode cockpit projection: %v\nbody: %s", err, body)
	}
	return got
}
