// escalate.go serves POST /api/v1/tasks/{id}/escalate (025 §8.1). The handler
// owns one decision the store does not: which document the escalation is
// against when the caller does not name one.
package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// escalateTask handles POST /api/v1/tasks/{id}/escalate: release the caller's
// lease, mint (or join) the design task that owes the fix, and block this task
// on it. The response is the store's result — the minted task, or the id of
// the escalation it joined.
func (s *server) escalateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.EscalateTaskInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	req.To = strings.TrimSpace(req.To)
	if req.To != "plan" && req.To != "spec" {
		writeErr(w, http.StatusUnprocessableEntity, `to must be "plan" or "spec"`)
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "reason is required: it is what the fixer reads")
		return
	}

	docID, err := s.escalationTarget(r, id, req)
	if err != nil {
		var amb *ambiguousTargetError
		if errors.As(err, &amb) {
			writeErr(w, http.StatusUnprocessableEntity, amb.Error())
			return
		}
		s.mapStoreErr(w, err)
		return
	}

	res, err := s.st.EscalateTask(r.Context(), store.EscalateInput{
		TaskID:  id,
		To:      req.To,
		DocID:   docID,
		Anchor:  strings.TrimSpace(req.Anchor),
		Reason:  req.Reason,
		ActorID: actorIDFrom(r),
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.EscalateTaskResult{Minted: res.Minted, Joined: res.Joined})
}

// ambiguousTargetError is a `--to spec` escalation from a plan covering more
// than one spec: the server will not pick, and names what the caller must
// choose between. It is a 422 rather than a store sentinel because the
// ambiguity is in the request, and only this handler can see it.
type ambiguousTargetError struct{ msg string }

func (e *ambiguousTargetError) Error() string { return e.msg }

// escalationTarget resolves the document the escalation is against. An
// explicit ref wins. Otherwise `--to plan` takes the task's own plan document,
// and `--to spec` takes the single spec that plan covers — refusing, rather
// than guessing, when the plan covers several.
func (s *server) escalationTarget(r *http.Request, taskID string, req model.EscalateTaskInput) (int64, error) {
	if ref := strings.TrimSpace(req.Doc); ref != "" {
		d, err := s.st.ResolveDocRef(r.Context(), ref)
		if err != nil {
			return 0, err
		}
		return d.ID, nil
	}
	t, err := s.st.GetTask(r.Context(), taskID)
	if err != nil {
		return 0, err
	}
	if t.PlanDoc == 0 {
		return 0, &ambiguousTargetError{"task " + taskID +
			" was not minted from a plan, so there is no document to escalate against: name one with --doc"}
	}
	if req.To == "plan" {
		return t.PlanDoc, nil
	}

	out, _, err := s.st.ListDocEdges(r.Context(), t.PlanDoc)
	if err != nil {
		return 0, err
	}
	var covered []model.DocEdge
	seen := map[int64]bool{}
	for _, e := range out {
		if e.Type != "covers" || e.ToDoc == 0 || seen[e.ToDoc] {
			continue
		}
		seen[e.ToDoc] = true
		covered = append(covered, e)
	}
	switch len(covered) {
	case 1:
		return covered[0].ToDoc, nil
	case 0:
		return 0, &ambiguousTargetError{"task " + taskID +
			"'s plan covers no spec in this backbone: name the spec with --doc"}
	default:
		names := make([]string, 0, len(covered))
		for _, e := range covered {
			names = append(names, e.ToSlug)
		}
		return 0, &ambiguousTargetError{"task " + taskID + "'s plan covers " +
			strings.Join(names, ", ") + ": say which with --doc"}
	}
}
