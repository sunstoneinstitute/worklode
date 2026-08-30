package api

import (
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// toBriefJSON fills every field except Skills.Matches: those come from
// skillMatches, which taskBrief calls once pins are known so a pinned skill
// is excluded from its own matches.
func toBriefJSON(b *store.Brief) model.Brief {
	out := model.Brief{
		Task:               b.Task,
		Body:               b.Body,
		Branch:             b.Branch,
		OpenBlockers:       make([]model.BriefBlocker, 0, len(b.OpenBlockers)),
		BlockingPlans:      append(make([]model.DocRef, 0, len(b.BlockingPlans)), b.BlockingPlans...),
		GoverningDesign:    b.GoverningDesign,
		AffectedComponents: b.AffectedComponents,
		DefinitionOfDone:   b.DefinitionOfDone,
		Blobs:              make([]model.TaskBlob, 0, len(b.Blobs)),
		Skills: model.SkillRecommendation{
			Pinned:   make([]model.PinnedSkill, 0, len(b.PinnedSkills)),
			Matches:  []model.SkillMatch{},
			Warnings: append([]string{}, b.SkillWarnings...),
			Provider: "none",
		},
	}
	for i := range b.OpenBlockers {
		blk := &b.OpenBlockers[i]
		out.OpenBlockers = append(out.OpenBlockers, model.BriefBlocker{
			ID: blk.ID, Title: blk.Title, State: blk.State,
		})
	}
	if b.Parent != nil {
		out.Parent = &model.TaskParent{ID: b.Parent.ID, Title: b.Parent.Title, State: b.Parent.State}
	}
	if b.Lease != nil {
		l := toLeaseJSON(b.Lease)
		out.Lease = &l
	}
	for _, sk := range b.PinnedSkills {
		out.Skills.Pinned = append(out.Skills.Pinned, toPinnedSkillJSON(sk))
	}
	out.Blobs = append(out.Blobs, b.Blobs...)
	return out
}

// taskBrief handles GET /api/v1/tasks/{id}/brief: the bounded start-of-work
// payload for a task (task row, branch, open blockers, active lease, and
// pinned/recommended skills). Pins are already resolved by store.Brief, so
// this calls skillMatches directly (excluding the pinned names) instead of
// recommendation, which would re-resolve the same pins.
func (s *server) taskBrief(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// skills=false is for callers that want the task row or the lease and
	// nothing else (lode worktree status, the pre-renew fetch in lode worktree
	// resume). It skips pin resolution, the inlined bodies, and the embedding
	// round trip.
	withSkills := r.URL.Query().Get("skills") != "false"
	b, err := s.st.Brief(r.Context(), id, store.BriefOptions{Skills: withSkills})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out := toBriefJSON(b)
	// Absolute, not root-relative: an agent fetching a brief is not
	// same-origin with the server and has nothing to resolve /blob/ against.
	base := strings.TrimRight(s.cfg.PublicURL, "/")
	for i := range out.Blobs {
		out.Blobs[i].URL = base + blobURL(out.Blobs[i].Hash, out.Blobs[i].Filename)
	}
	if !withSkills {
		writeJSON(w, http.StatusOK, out)
		return
	}

	pinnedNames := make(map[string]bool, len(b.PinnedSkills))
	for _, sk := range b.PinnedSkills {
		pinnedNames[sk.QualifiedName()] = true
	}
	if s.embedder != nil {
		out.Skills.Provider = "openai-compatible"
	}
	matches, warnings := s.skillMatches(r.Context(), b.Task.Title+"\n\n"+b.Body, pinnedNames, 0)
	out.Skills.Matches = matches
	out.Skills.Warnings = append(out.Skills.Warnings, warnings...)

	writeJSON(w, http.StatusOK, out)
}

// rebindWorktree handles POST /api/v1/tasks/{id}/lease/worktree: move the
// caller's active lease to a new worktree. A non-holder gets 404 (same
// probe-resistant policy as renew/release); a worktree already holding another
// active lease gets 409. On success the updated lease is returned so the caller
// can confirm the new binding.
func (s *server) rebindWorktree(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.RebindWorktreeInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Worktree == "" {
		writeErr(w, http.StatusBadRequest, "worktree is required")
		return
	}
	actorID := actorIDFrom(r)

	lease, err := s.st.RebindLeaseWorktree(r.Context(), id, actorID, req.Worktree)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toLeaseJSON(lease))
}
