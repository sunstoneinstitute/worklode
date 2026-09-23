// projectsettings.go: PATCH /api/v1/projects/{id}/settings (increment 3
// R9) — a partial update to the small per-project override set behind
// store.SetProjectSettings's server-side key allowlist.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// patchProjectSettings handles PATCH /api/v1/projects/{id}/settings. The
// body is a JSON object whose values are decoded as raw JSON so a null
// literal (remove the key) survives distinguishable from an absent one; the
// store's allowlist refuses an unknown key or a wrong-shaped value with a
// 422 naming it (mapStoreErr, store.ErrInvalidInput). Guarded the same as
// PATCH /api/v1/projects/{id} (permProjectAdmin): settings affect every
// caller's plan writes, not just the one making the change.
func (s *server) patchProjectSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.ProjectSettingsInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	patch := map[string]json.RawMessage(req)
	// Refuse a patch that would leave the soft plan-token budget above the
	// hard ceiling (M1): the soft warning could then never fire, whether
	// the offending hard comes from this same patch, from the project's
	// own already-stored setting, or from the instance default.
	soft, hard, err := s.planBudgetAfter(r.Context(), id, patch)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if soft > hard {
		s.mapStoreErr(w, fmt.Errorf(
			"plan_tokens_soft (%d) would exceed plan_tokens_hard (%d): %w", soft, hard, store.ErrInvalidInput))
		return
	}
	if err := s.st.SetProjectSettings(r.Context(), id, patch); err != nil {
		s.mapStoreErr(w, err)
		return
	}

	p, err := s.st.GetProject(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	repos, err := s.st.ListRepos(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectJSON(p, repos))
}
