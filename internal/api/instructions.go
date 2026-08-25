package api

import (
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// enqueueInstruction handles POST /api/v1/tasks/{id}/instructions: queue a
// steering instruction against the task, delivered to whichever actor next
// holds its lease (migration 0052).
func (s *server) enqueueInstruction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.InstructionInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "body is required")
		return
	}
	instr, err := s.st.EnqueueInstruction(r.Context(), id, actorIDFrom(r), req.Body)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, instr)
}

// claimInstructions handles POST /api/v1/instructions/claim: deliver every
// pending instruction queued against a task the caller currently leases.
// Actor-scoped rather than task-scoped — there is no task in the path, only
// the caller's own identity (see requireTaskScope and guardedAny in
// router.go) — so a task-scoped token minted for one task's worktree may
// poll this route for the instructions queued against that same task.
func (s *server) claimInstructions(w http.ResponseWriter, r *http.Request) {
	instructions, err := s.st.ClaimPendingInstructionsForActor(r.Context(), actorIDFrom(r))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if instructions == nil {
		instructions = []model.Instruction{}
	}
	writeJSON(w, http.StatusOK, model.InstructionsResponse{Instructions: instructions})
}
