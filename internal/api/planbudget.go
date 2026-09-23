// planbudget.go: the plan document token budget (S6, S19) — a soft warning
// and a hard refusal on a plan body's size, checked by createDoc and
// updateDocBody (docs.go), with a per-project override in projects.settings
// (internal/store/projectsettings.go).
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// defaultPlanTokensSoft and defaultPlanTokensHard are the S19 defaults, used
// when the operator sets neither LODE_PLAN_TOKENS_SOFT nor
// LODE_PLAN_TOKENS_HARD.
const (
	defaultPlanTokensSoft = 32000
	defaultPlanTokensHard = 64000
)

// planBudget parses the plan token budget from config (S6, S19): soft 32000
// and hard 64000 unless set; both must be positive, and soft may not exceed
// hard.
func planBudget(cfg Config) (soft, hard int, err error) {
	soft, hard = defaultPlanTokensSoft, defaultPlanTokensHard
	if cfg.PlanTokensSoft != "" {
		soft, err = strconv.Atoi(cfg.PlanTokensSoft)
		if err != nil || soft <= 0 {
			return 0, 0, fmt.Errorf("LODE_PLAN_TOKENS_SOFT: want a positive integer, got %q", cfg.PlanTokensSoft)
		}
	}
	if cfg.PlanTokensHard != "" {
		hard, err = strconv.Atoi(cfg.PlanTokensHard)
		if err != nil || hard <= 0 {
			return 0, 0, fmt.Errorf("LODE_PLAN_TOKENS_HARD: want a positive integer, got %q", cfg.PlanTokensHard)
		}
	}
	if soft > hard {
		return 0, 0, fmt.Errorf("LODE_PLAN_TOKENS_SOFT (%d) exceeds LODE_PLAN_TOKENS_HARD (%d)", soft, hard)
	}
	return soft, hard, nil
}

// planTokens estimates a body's tokens with the inverse of
// corpusindex.BudgetFor's runes-per-token ratio (7 runes to 4 tokens).
func planTokens(body string) int {
	return utf8.RuneCountInString(body) * 4 / 7
}

// planBudgetFor is the server budget with the project's settings overriding
// either bound.
func (s *server) planBudgetFor(ctx context.Context, projectID string) (soft, hard int, err error) {
	soft, hard = s.planSoft, s.planHard
	p, err := s.st.GetProject(ctx, projectID)
	if err != nil {
		return 0, 0, err
	}
	if v, ok := settingsInt(p.Settings, "plan_tokens_soft"); ok {
		soft = v
	}
	if v, ok := settingsInt(p.Settings, "plan_tokens_hard"); ok {
		hard = v
	}
	return soft, hard, nil
}

// settingsInt reads an integer-valued key out of a project's settings
// (store.Project.Settings, one json.RawMessage per key). A missing key or one
// that isn't a whole number reports ok=false, so the caller's default stands
// — store.SetProjectSettings already refused anything else at write time.
func settingsInt(settings map[string]json.RawMessage, key string) (int, bool) {
	raw, ok := settings[key]
	if !ok {
		return 0, false
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}

// planBudgetAfter computes the soft/hard bounds a settings patch would leave
// in effect, mixing patch over the project's current settings over the
// instance default (planSoft/planHard) — the same fallback order
// planBudgetFor applies to a stored row. Computed before the write, so
// patchProjectSettings can refuse a patch that would leave soft above hard
// (M1) rather than silently landing a budget whose warning can never fire —
// whether the offending hard comes from the patch itself, from the
// project's own already-stored setting, or from the instance default.
func (s *server) planBudgetAfter(ctx context.Context, projectID string, patch map[string]json.RawMessage) (soft, hard int, err error) {
	soft, hard = s.planSoft, s.planHard
	p, err := s.st.GetProject(ctx, projectID)
	if err != nil {
		return 0, 0, err
	}
	merged := make(map[string]json.RawMessage, len(p.Settings)+len(patch))
	for k, v := range p.Settings {
		merged[k] = v
	}
	for k, v := range patch {
		if string(v) == "null" {
			delete(merged, k)
			continue
		}
		merged[k] = v
	}
	if v, ok := settingsInt(merged, "plan_tokens_soft"); ok {
		soft = v
	}
	if v, ok := settingsInt(merged, "plan_tokens_hard"); ok {
		hard = v
	}
	return soft, hard, nil
}

// checkPlanBudget refuses a plan body over the hard ceiling and returns the
// soft warning when over the soft one. Only plan bodies are measured; every
// other kind returns no warnings and no error, and observes no metric — a
// spec or ADR write never contributes a data point to
// worklode_plan_budget_checks_total.
func (s *server) checkPlanBudget(ctx context.Context, kind, projectID, body string) (warnings []string, err error) {
	if kind != "plan" {
		return nil, nil
	}
	soft, hard, err := s.planBudgetFor(ctx, projectID)
	if err != nil {
		return nil, err
	}
	n := planTokens(body)
	if n > hard {
		s.observePlanBudgetCheck(planBudgetCheckRefused)
		return nil, fmt.Errorf("plan body is about %d tokens, over the hard ceiling of %d (12 S19); split it (lode:splitting-specs-into-plans): %w",
			n, hard, store.ErrInvalidInput)
	}
	if n > soft {
		s.observePlanBudgetCheck(planBudgetCheckWarn)
		warnings = append(warnings, fmt.Sprintf("plan body is about %d tokens, over the soft budget of %d (12 S19); consider splitting it", n, soft))
		return warnings, nil
	}
	s.observePlanBudgetCheck(planBudgetCheckOK)
	return warnings, nil
}
