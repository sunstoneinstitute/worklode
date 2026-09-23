// replan.go: POST /api/v1/work/replan (S28) — the deliberate `lode work next
// --replan` door onto a stale plan document; see store.ReplanNext.
package api

import (
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// replan handles POST /api/v1/work/replan: hand a stale plan out as a
// claimed design task (S28), reusing the one the §8.7 doc-lifecycle
// rule minted when there is one. Project is required; Plan is optional and, when set, must name a
// stale plan or the call is refused (store.ReplanNext's ErrInvalidInput).
// The response renders through writeClaimNextResult, the same conversion
// claimNext uses, so a replanned claim looks like an ordinary one.
func (s *server) replan(w http.ResponseWriter, r *http.Request) {
	var req model.ReplanInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Project == "" {
		writeErr(w, http.StatusBadRequest, "project is required")
		return
	}
	if req.Worktree == "" {
		writeErr(w, http.StatusBadRequest, "worktree is required")
		return
	}
	actorID := actorIDFrom(r)

	res, err := s.st.ReplanNext(r.Context(), store.ReplanOpts{
		ProjectID: req.Project,
		Plan:      req.Plan,
		ActorID:   actorID,
		Worktree:  req.Worktree,
		TTL:       time.Duration(req.TTLSeconds) * time.Second,
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.writeClaimNextResult(w, r, res, false, "no stale plan")
}
