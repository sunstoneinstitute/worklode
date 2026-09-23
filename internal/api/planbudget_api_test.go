package api_test

// planbudget_api_test.go covers the plan token budget's wiring into the
// document write handlers (S6, S19): createDoc and updateDocBody refuse a
// plan body over the hard ceiling and warn over the soft one. The budget
// arithmetic itself (planTokens, planBudget, planBudgetFor, checkPlanBudget)
// is covered white-box in internal/api/planbudget_test.go (package api);
// this file only exercises the HTTP-visible behavior docs_test.go's other
// tests already establish the pattern for.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// planBudgetBody is a minimal, valid plan document (no anchor lint applies
// to plans) padded to a controlled rune count, so its token estimate
// (runes*4/7) lands where the test wants it relative to the configured
// soft/hard budget.
func planBudgetBody(totalRunes int) string {
	const head = "# Plan\n\n"
	pad := totalRunes - len([]rune(head))
	if pad < 0 {
		pad = 0
	}
	return head + strings.Repeat("x", pad)
}

func TestCreateDocPlanTokenBudget(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServerWithConfig(t, api.Config{
		WebOpen: true, PlanTokensSoft: "50", PlanTokensHard: "100",
	})
	createProject(t, st, "proj")

	// Clean: well under the soft budget (a 50-rune body is about 28 tokens).
	clean := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "clean-plan", Body: planBudgetBody(50),
	})
	if len(clean.Warnings) != 0 {
		t.Errorf("clean plan: warnings = %v, want none", clean.Warnings)
	}

	// Over soft (50), under hard (100): a warning, still created.
	warned := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "warned-plan", Body: planBudgetBody(120),
	})
	if len(warned.Warnings) != 1 {
		t.Fatalf("warned plan: warnings = %v, want one", warned.Warnings)
	}

	// Over hard (100): refused.
	rr := doReq(t, h, "POST", "/api/v1/docs", token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "over-hard-plan", Body: planBudgetBody(300),
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-hard plan status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}

	// A spec of the same size is never measured.
	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Slug: "big-spec", Body: docSpecBody + strings.Repeat("x", 500),
	})
	if len(spec.Warnings) != 0 {
		t.Errorf("spec: warnings = %v, want none (specs are not measured)", spec.Warnings)
	}

	// A project override on plan_tokens_hard lifts the refusal.
	rr = doReq(t, h, "PATCH", "/api/v1/projects/proj/settings", token,
		map[string]any{"plan_tokens_hard": 1000})
	if rr.Code != http.StatusOK {
		t.Fatalf("set plan_tokens_hard status = %d, body %s", rr.Code, rr.Body.String())
	}
	lifted := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "lifted-plan", Body: planBudgetBody(300),
	})
	if len(lifted.Warnings) != 1 {
		t.Errorf("lifted plan: warnings = %v, want one (still over the soft budget)", lifted.Warnings)
	}
}

func TestUpdateDocBodyPlanTokenBudget(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServerWithConfig(t, api.Config{
		WebOpen: true, PlanTokensSoft: "50", PlanTokensHard: "100",
	})
	createProject(t, st, "proj")

	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "editable-plan", Body: planBudgetBody(50),
	})

	rr := doReq(t, h, "PUT", docPath(plan.ID, "/body"), token,
		model.UpdateDocBodyInput{Body: planBudgetBody(300)})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-hard edit status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "PUT", docPath(plan.ID, "/body"), token,
		model.UpdateDocBodyInput{Body: planBudgetBody(120)})
	if rr.Code != http.StatusOK {
		t.Fatalf("over-soft edit status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got model.Doc
	decodeInto(t, rr, &got)
	if len(got.Warnings) != 1 {
		t.Fatalf("over-soft edit: warnings = %v, want one", got.Warnings)
	}
}

// TestPatchProjectSettingsRefusesSoftAboveHard covers M1: a settings patch
// that would leave the effective plan_tokens_soft above plan_tokens_hard is
// refused (422) rather than landing a budget whose soft warning can never
// fire — whichever of the three places the offending hard bound comes from.
func TestPatchProjectSettingsRefusesSoftAboveHard(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServerWithConfig(t, api.Config{
		WebOpen: true, PlanTokensSoft: "50", PlanTokensHard: "100",
	})
	createProject(t, st, "proj")

	// Directly: the patch itself states both, soft above hard.
	rr := doReq(t, h, "PATCH", "/api/v1/projects/proj/settings", token,
		map[string]any{"plan_tokens_soft": 200, "plan_tokens_hard": 100})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("direct: status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}

	// Against the instance default: the patch touches only soft, and no
	// hard is stored for this project, so the instance default (100) is
	// the effective hard.
	rr = doReq(t, h, "PATCH", "/api/v1/projects/proj/settings", token,
		map[string]any{"plan_tokens_soft": 150})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("against instance default: status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}

	// Against the project's own stored hard: first land a valid hard
	// override, then patch only soft above it.
	rr = doReq(t, h, "PATCH", "/api/v1/projects/proj/settings", token,
		map[string]any{"plan_tokens_hard": 60})
	if rr.Code != http.StatusOK {
		t.Fatalf("store a hard override: status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "PATCH", "/api/v1/projects/proj/settings", token,
		map[string]any{"plan_tokens_soft": 80})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("against stored hard: status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}

	// A patch that leaves soft at or under hard still succeeds.
	rr = doReq(t, h, "PATCH", "/api/v1/projects/proj/settings", token,
		map[string]any{"plan_tokens_soft": 40})
	if rr.Code != http.StatusOK {
		t.Fatalf("valid patch: status = %d, body %s", rr.Code, rr.Body.String())
	}
}
