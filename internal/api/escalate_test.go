package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// escalateSpecBody is a spec with one anchored section, for a plan to cover.
const escalateSpecBody = `---
status: draft
---

# A spec

## 1. Scope {#sec-1}

Scope body.
`

// escalatePlanBody is docPlanMintBody plus a covers list: a plan that mints
// tasks and points at the specs it implements, which is what `--to spec` walks.
func escalatePlanBody(covered ...string) string {
	var b strings.Builder
	b.WriteString("---\nstatus: draft\ncovers:\n")
	for _, slug := range covered {
		b.WriteString("  - " + slug + ".md#sec-1\n")
	}
	b.WriteString("---\n\n# A covering plan\n\n## Tasks\n\n### Task 1 — First task\n\n")
	b.WriteString("```yaml\nkind: feature\npriority: high\n```\n\nDo the first thing.\n")
	return b.String()
}

// escalateFixture creates the specs, the plan that covers them, and the task
// the caller escalates from — claimed, because escalating is an act of the
// holder. It returns the plan, the specs, and the claimed task.
func escalateFixture(t *testing.T, h http.Handler, token string, specSlugs ...string) (model.Doc, []model.Doc, model.Task) {
	t.Helper()
	var specs []model.Doc
	for _, slug := range specSlugs {
		specs = append(specs, createDocViaAPI(t, h, token, model.CreateDocInput{
			Project: "proj", Kind: "spec", Slug: slug, Body: escalateSpecBody,
		}))
	}
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "escalate-plan",
		Body: escalatePlanBody(specSlugs...),
	})
	rr := doReq(t, h, "POST", docPath(plan.ID, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept plan status = %d, body %s", rr.Code, rr.Body.String())
	}
	var resp model.AcceptDocResponse
	decodeInto(t, rr, &resp)
	if len(resp.Tasks) == 0 {
		t.Fatalf("plan minted no tasks")
	}
	task := resp.Tasks[0]
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+task.ID+"/claim", token,
		map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}
	return plan, specs, task
}

// TestEscalateTaskToPlan is the default target: no --doc, so the escalation
// lands on the plan the task was minted from (025 §8.1).
func TestEscalateTaskToPlan(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	plan, _, task := escalateFixture(t, h, token, "escalate-spec")

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+task.ID+"/escalate", token,
		model.EscalateTaskInput{To: "plan", Anchor: "sec-2", Reason: "the plan skips the empty case"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var res model.EscalateTaskResult
	decodeInto(t, rr, &res)
	if res.Minted == nil || res.Joined != "" {
		t.Fatalf("result = %+v, want a minted task", res)
	}
	if res.Minted.AboutDoc != plan.ID || res.Minted.AboutAnchor != "sec-2" {
		t.Errorf("minted task is about doc %d anchor %q, want %d sec-2",
			res.Minted.AboutDoc, res.Minted.AboutAnchor, plan.ID)
	}
	if res.Minted.Kind != "design" {
		t.Errorf("minted task kind = %q, want design", res.Minted.Kind)
	}

	// Escalating hands the task back, so the same caller cannot escalate
	// again without claiming it: the missing lease is a 404, not a 500.
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+task.ID+"/escalate", token,
		model.EscalateTaskInput{To: "plan", Anchor: "sec-2", Reason: "still skipped"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("re-escalating without the lease: status = %d, body %s", rr.Code, rr.Body.String())
	}
}

// TestEscalateTaskToSpec is the handler's own resolution: `--to spec` walks
// the plan's covers edges, and takes the single spec it finds.
func TestEscalateTaskToSpec(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	_, specs, task := escalateFixture(t, h, token, "escalate-spec")

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+task.ID+"/escalate", token,
		model.EscalateTaskInput{To: "spec", Reason: "the spec says nothing about this"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var res model.EscalateTaskResult
	decodeInto(t, rr, &res)
	if res.Minted == nil {
		t.Fatalf("result = %+v, want a minted task", res)
	}
	if res.Minted.AboutDoc != specs[0].ID {
		t.Errorf("minted task is about doc %d, want the spec %d", res.Minted.AboutDoc, specs[0].ID)
	}
}

// TestEscalateTaskToSpecAmbiguous: a plan covering two specs is a choice the
// server will not make. 422, naming both candidates.
func TestEscalateTaskToSpecAmbiguous(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	_, _, task := escalateFixture(t, h, token, "escalate-spec-a", "escalate-spec-b")

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+task.ID+"/escalate", token,
		model.EscalateTaskInput{To: "spec", Reason: "which one owes this?"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"escalate-spec-a", "escalate-spec-b", "--doc"} {
		if !strings.Contains(body, want) {
			t.Errorf("422 body %q does not name %q", body, want)
		}
	}
}

// TestEscalateTaskRefusesBadRequest covers the handler's gates: the tier must
// be one of two words, the reason is what the fixer reads, and a task minted
// from no plan has no default target.
func TestEscalateTaskRefusesBadRequest(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	_, _, task := escalateFixture(t, h, token, "escalate-spec")

	for _, tc := range []struct {
		name string
		in   model.EscalateTaskInput
	}{
		{"unknown tier", model.EscalateTaskInput{To: "vibes", Reason: "why"}},
		{"blank reason", model.EscalateTaskInput{To: "plan", Reason: "  "}},
	} {
		rr := doReq(t, h, "POST", "/api/v1/tasks/"+task.ID+"/escalate", token, tc.in)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, body %s", tc.name, rr.Code, rr.Body.String())
		}
	}

	// A task with no plan behind it: nothing to default to.
	free := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Freestanding", "kind": "feature", "priority": "medium",
	})
	id, _ := free["id"].(string)
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/claim", token,
		map[string]any{"worktree": "host:/wt-2"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/escalate", token,
		model.EscalateTaskInput{To: "plan", Reason: "no plan to escalate to"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no-plan escalation: status = %d, body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "--doc") {
		t.Errorf("422 body %q does not point at --doc", rr.Body.String())
	}
}
