package api

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestPlanBudget covers S19's config parsing: defaults, both bounds set,
// invalid values, and soft exceeding hard.
func TestPlanBudget(t *testing.T) {
	cases := []struct {
		name       string
		cfg        Config
		soft, hard int
		wantErr    bool
	}{
		{"defaults", Config{}, defaultPlanTokensSoft, defaultPlanTokensHard, false},
		{"both set", Config{PlanTokensSoft: "10", PlanTokensHard: "20"}, 10, 20, false},
		{"soft only", Config{PlanTokensSoft: "10"}, 10, defaultPlanTokensHard, false},
		{"hard only", Config{PlanTokensHard: "40000"}, defaultPlanTokensSoft, 40000, false},
		{"soft not a number", Config{PlanTokensSoft: "big"}, 0, 0, true},
		{"hard not a number", Config{PlanTokensHard: "big"}, 0, 0, true},
		{"soft zero", Config{PlanTokensSoft: "0"}, 0, 0, true},
		{"soft above hard", Config{PlanTokensSoft: "100", PlanTokensHard: "50"}, 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			soft, hard, err := planBudget(c.cfg)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if err != nil {
				return
			}
			if soft != c.soft || hard != c.hard {
				t.Errorf("soft/hard = %d/%d, want %d/%d", soft, hard, c.soft, c.hard)
			}
		})
	}
}

// TestPlanTokens covers the 7-runes-to-4-tokens ratio, the inverse of
// corpusindex.BudgetFor.
func TestPlanTokens(t *testing.T) {
	if got := planTokens(""); got != 0 {
		t.Errorf("planTokens(\"\") = %d, want 0", got)
	}
	if got := planTokens(strings.Repeat("a", 7000)); got != 4000 {
		t.Errorf("planTokens(7000 runes) = %d, want 4000", got)
	}
}

// TestPlanBudgetFor covers the per-project override (S6, S19): the instance
// default applies with no project settings, and a project's
// plan_tokens_hard/plan_tokens_soft override either bound independently.
func TestPlanBudgetFor(t *testing.T) {
	ctx := t.Context()
	st := store.OpenTestStore(t)
	if err := st.CreateProject(ctx, "proj", "proj", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	s := &server{st: st, planSoft: 10, planHard: 20}

	soft, hard, err := s.planBudgetFor(ctx, "proj")
	if err != nil {
		t.Fatalf("planBudgetFor: %v", err)
	}
	if soft != 10 || hard != 20 {
		t.Fatalf("soft/hard = %d/%d, want 10/20 (instance default)", soft, hard)
	}

	if err := st.SetProjectSettings(ctx, "proj", settingsPatch(t, "plan_tokens_hard", 1000)); err != nil {
		t.Fatalf("set project settings: %v", err)
	}
	soft, hard, err = s.planBudgetFor(ctx, "proj")
	if err != nil {
		t.Fatalf("planBudgetFor: %v", err)
	}
	if soft != 10 || hard != 1000 {
		t.Fatalf("soft/hard = %d/%d, want 10/1000 (hard overridden)", soft, hard)
	}
}

// settingsPatch is a one-key SetProjectSettings patch.
func settingsPatch(t *testing.T, key string, value int) map[string]json.RawMessage {
	t.Helper()
	return map[string]json.RawMessage{key: json.RawMessage(strconv.Itoa(value))}
}

// TestCheckPlanBudget covers the wiring createDoc/updateDocBody rely on: a
// non-plan kind is never checked, a plan under the soft budget is clean, one
// over it warns, and one over the hard ceiling is refused with
// store.ErrInvalidInput (mapStoreErr's 422).
func TestCheckPlanBudget(t *testing.T) {
	ctx := t.Context()
	st := store.OpenTestStore(t)
	if err := st.CreateProject(ctx, "proj", "proj", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	// soft=10 tokens, hard=20 tokens.
	s := &server{st: st, planSoft: 10, planHard: 20}

	// A spec is never measured, however long.
	warnings, err := s.checkPlanBudget(ctx, "spec", "proj", strings.Repeat("a", 100))
	if err != nil || warnings != nil {
		t.Fatalf("spec: warnings=%v err=%v, want nil/nil", warnings, err)
	}

	// 30 runes -> 17 tokens: over soft (10), under hard (20): a warning.
	warnings, err = s.checkPlanBudget(ctx, "plan", "proj", strings.Repeat("a", 30))
	if err != nil {
		t.Fatalf("30 runes: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("30 runes: warnings = %v, want one", warnings)
	}

	// 100 runes -> 57 tokens: over hard (20): refused.
	_, err = s.checkPlanBudget(ctx, "plan", "proj", strings.Repeat("a", 100))
	if !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("100 runes: err = %v, want ErrInvalidInput", err)
	}

	// A project override lifts the refusal.
	if err := st.SetProjectSettings(ctx, "proj", settingsPatch(t, "plan_tokens_hard", 1000)); err != nil {
		t.Fatalf("set project settings: %v", err)
	}
	warnings, err = s.checkPlanBudget(ctx, "plan", "proj", strings.Repeat("a", 100))
	if err != nil {
		t.Fatalf("100 runes with raised hard budget: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("100 runes with raised hard budget: warnings = %v, want one (still over the 10-token soft budget)", warnings)
	}
}

// TestCheckPlanBudgetMetric covers worklode_plan_budget_checks_total
// (WL-SPEC-22): each of the three outcomes — ok, warn, refused — increments
// its own label exactly once, and a non-plan kind observes nothing at all.
func TestCheckPlanBudgetMetric(t *testing.T) {
	ctx := t.Context()
	st := store.OpenTestStore(t)
	if err := st.CreateProject(ctx, "proj", "proj", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	reg := prometheus.NewRegistry()
	// soft=10 tokens, hard=20 tokens.
	s := &server{st: st, planSoft: 10, planHard: 20}
	s.initMetrics(reg)

	if _, err := s.checkPlanBudget(ctx, "spec", "proj", strings.Repeat("a", 100)); err != nil {
		t.Fatalf("spec: %v", err)
	}
	// 10 runes -> 5 tokens: under soft (10): ok.
	if _, err := s.checkPlanBudget(ctx, "plan", "proj", strings.Repeat("a", 10)); err != nil {
		t.Fatalf("ok plan: %v", err)
	}
	// 30 runes -> 17 tokens: over soft (10), under hard (20): warn.
	if _, err := s.checkPlanBudget(ctx, "plan", "proj", strings.Repeat("a", 30)); err != nil {
		t.Fatalf("warn plan: %v", err)
	}
	// 100 runes -> 57 tokens: over hard (20): refused.
	if _, err := s.checkPlanBudget(ctx, "plan", "proj", strings.Repeat("a", 100)); err == nil {
		t.Fatal("refused plan: want an error")
	}

	for _, tc := range []struct {
		outcome string
		want    float64
	}{
		{planBudgetCheckOK, 1},
		{planBudgetCheckWarn, 1},
		{planBudgetCheckRefused, 1},
	} {
		if got := testutil.ToFloat64(s.planBudgetChecks.WithLabelValues(tc.outcome)); got != tc.want {
			t.Errorf("planBudgetChecks{outcome=%s} = %v, want %v", tc.outcome, got, tc.want)
		}
	}
}

// TestPlanBudgetAfter covers M1: the effective soft/hard a settings patch
// would leave in place, mixing the patch over the project's already-stored
// settings over the instance default — the basis patchProjectSettings
// refuses a patch on when soft would end up above hard.
func TestPlanBudgetAfter(t *testing.T) {
	ctx := t.Context()
	st := store.OpenTestStore(t)
	if err := st.CreateProject(ctx, "proj", "proj", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	s := &server{st: st, planSoft: 50, planHard: 100}

	// No patch, nothing stored: the instance default.
	soft, hard, err := s.planBudgetAfter(ctx, "proj", nil)
	if err != nil {
		t.Fatalf("planBudgetAfter: %v", err)
	}
	if soft != 50 || hard != 100 {
		t.Fatalf("soft/hard = %d/%d, want 50/100 (instance default)", soft, hard)
	}

	// A patch setting only soft, above the instance default hard.
	soft, hard, err = s.planBudgetAfter(ctx, "proj", settingsPatch(t, "plan_tokens_soft", 150))
	if err != nil {
		t.Fatalf("planBudgetAfter: %v", err)
	}
	if soft != 150 || hard != 100 {
		t.Fatalf("soft/hard = %d/%d, want 150/100", soft, hard)
	}

	// Store a hard override, then patch only soft above it.
	if err := st.SetProjectSettings(ctx, "proj", settingsPatch(t, "plan_tokens_hard", 60)); err != nil {
		t.Fatalf("set project settings: %v", err)
	}
	soft, hard, err = s.planBudgetAfter(ctx, "proj", settingsPatch(t, "plan_tokens_soft", 80))
	if err != nil {
		t.Fatalf("planBudgetAfter: %v", err)
	}
	if soft != 80 || hard != 60 {
		t.Fatalf("soft/hard = %d/%d, want 80/60 (stored hard, patched soft)", soft, hard)
	}
}
