//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

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
// same thing, because each recomputes the same derivation per call.
func TestProgressOverAMintedPlan(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken, WebOpen: true})
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
