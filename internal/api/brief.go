package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// briefBlockerJSON is the slim projection of an open blocker in a brief: just
// the fields an agent needs to see why a task is blocked.
type briefBlockerJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// briefJSON is the wire form of a task brief. lease is null when the task has
// no active lease. open_blockers is always an array (never null). The three
// reserved fields serialize as JSON null in v1: governing_design and
// definition_of_done are *string, affected_components is a nil []string
// (marshals to null, not []) — see store.Brief.
type briefJSON struct {
	Task               taskJSON           `json:"task"`
	Body               string             `json:"body"`
	Branch             string             `json:"branch"`
	OpenBlockers       []briefBlockerJSON `json:"open_blockers"`
	Lease              *leaseJSON         `json:"lease"`
	GoverningDesign    *string            `json:"governing_design"`
	AffectedComponents []string           `json:"affected_components"`
	DefinitionOfDone   *string            `json:"definition_of_done"`
}

func toBriefJSON(b *store.Brief) briefJSON {
	out := briefJSON{
		Task:               toTaskJSON(&b.Task),
		Body:               b.Body,
		Branch:             b.Branch,
		OpenBlockers:       make([]briefBlockerJSON, 0, len(b.OpenBlockers)),
		GoverningDesign:    b.GoverningDesign,
		AffectedComponents: b.AffectedComponents,
		DefinitionOfDone:   b.DefinitionOfDone,
	}
	for i := range b.OpenBlockers {
		blk := &b.OpenBlockers[i]
		out.OpenBlockers = append(out.OpenBlockers, briefBlockerJSON{
			ID: blk.ID, Title: blk.Title, State: blk.State,
		})
	}
	if b.Lease != nil {
		l := toLeaseJSON(b.Lease)
		out.Lease = &l
	}
	return out
}

// taskBrief handles GET /api/v1/tasks/{id}/brief: the bounded start-of-work
// payload for a task (task row, branch, open blockers, active lease).
func (s *server) taskBrief(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := s.st.Brief(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBriefJSON(b))
}

type rebindWorktreeRequest struct {
	Worktree string `json:"worktree"`
}

// rebindWorktree handles POST /api/v1/tasks/{id}/lease/worktree: move the
// caller's active lease to a new worktree. A non-holder gets 404 (same
// probe-resistant policy as renew/release); a worktree already holding another
// active lease gets 409. On success the updated lease is returned so the caller
// can confirm the new binding.
func (s *server) rebindWorktree(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req rebindWorktreeRequest
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Worktree == "" {
		writeErr(w, http.StatusBadRequest, "worktree is required")
		return
	}
	actor := actorFrom(r)

	lease, err := s.st.RebindLeaseWorktree(r.Context(), id, actor.ID, req.Worktree)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toLeaseJSON(lease))
}
