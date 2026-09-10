// ladder.go serves POST /api/v1/tasks/{id}/gap and POST /api/v1/tasks/{id}/fix
// (025 §15.5): the escalation ladder's non-escalating rungs. Unlike
// escalate.go's route, neither writes anything about the task itself — no
// lease, no edge, no state change — so each handler validates its closed
// label set and calls straight through to one store.RecordEvent call.
package api

import (
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// reportGap handles POST /api/v1/tasks/{id}/gap: an executor found that the
// plan or spec does not cover the case in front of it, but kept going. The
// payload it logs is escalate's minus "to" — a gap recorded without
// stopping has no escalation target.
func (s *server) reportGap(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.GapTaskInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if _, err := s.st.GetTask(r.Context(), id); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if strings.TrimSpace(req.Doc) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "doc is required")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "reason is required: it is what the fixer reads")
		return
	}
	doc, err := s.st.ResolveDocRef(r.Context(), req.Doc)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	inserted, err := s.st.RecordGap(r.Context(), store.GapInput{
		TaskID: id, Doc: doc.Slug, Anchor: strings.TrimSpace(req.Anchor), Reason: req.Reason,
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.LadderEventResult{Recorded: inserted})
}

// reportFix handles POST /api/v1/tasks/{id}/fix: the fixer's start or finish
// of a design fix. Phase picks which of fix.started/fix.finished lands, and
// each phase's own required fields — a 422 on anything else, because the
// funnel is only worth graphing if the label set is closed (025 §15.5).
func (s *server) reportFix(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.FixTaskInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if _, err := s.st.GetTask(r.Context(), id); err != nil {
		s.mapStoreErr(w, err)
		return
	}

	var inserted bool
	switch strings.TrimSpace(req.Phase) {
	case "started":
		if req.Tier != "plan" && req.Tier != "spec" {
			writeErr(w, http.StatusUnprocessableEntity, `tier must be "plan" or "spec"`)
			return
		}
		if strings.TrimSpace(req.Doc) == "" {
			writeErr(w, http.StatusUnprocessableEntity, "doc is required")
			return
		}
		doc, err := s.st.ResolveDocRef(r.Context(), req.Doc)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		inserted, err = s.st.RecordFixStarted(r.Context(), store.FixStartedInput{
			TaskID: id, Tier: req.Tier, Doc: doc.Slug, Attempt: req.Attempt,
		})
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
	case "finished":
		switch req.Outcome {
		case "resolved", "substantive", "escalated":
		default:
			writeErr(w, http.StatusUnprocessableEntity,
				`outcome must be "resolved", "substantive" or "escalated"`)
			return
		}
		var err error
		inserted, err = s.st.RecordFixFinished(r.Context(), store.FixFinishedInput{
			TaskID: id, Outcome: req.Outcome, Attempt: req.Attempt,
		})
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
	default:
		writeErr(w, http.StatusUnprocessableEntity, `phase must be "started" or "finished"`)
		return
	}
	writeJSON(w, http.StatusOK, model.LadderEventResult{Recorded: inserted})
}
