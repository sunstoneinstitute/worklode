// softdelete.go serves spec 044's delete and undelete for tasks and design
// documents. Delete is a tombstone, not a state: the store hides the row from
// every list and keeps its events, edges and artifacts valid (044 §2).
//
// The one rule this layer owns is 044 §3's: whether a justification is
// required depends on LODE_INSTANCE_ENV (039 §3), and the server is the only
// party that knows which instance it is. A client may prompt for a
// justification; it must not decide the rule, so the check lives here and
// nowhere else.
package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// justificationRequiredMsg is the refusal a prod instance answers a blank
// delete justification with. It names the instance environment because the
// request is well-formed and the same server configured the other way would
// have accepted it (044 §5).
const justificationRequiredMsg = "justification is required on a prod instance (LODE_INSTANCE_ENV=prod)"

// requiresJustification reports whether this instance demands one. Anything
// that is not a dev instance is a prod instance: NewServer has already
// normalised the empty value to prod (039 §3), and a *server built directly in
// a test with no config is a prod instance for the same safety reason.
func (s *server) requiresJustification() bool {
	return s.cfg.InstanceEnv != InstanceDev
}

// deleteJustification reads the optional DeleteInput body off a DELETE and
// applies 044 §3. An absent body is legal — a bodyless DELETE is an ordinary
// request — and leaves the justification empty, which a dev instance accepts
// and a prod instance refuses with 422. The value is trimmed before both the
// blank check and storage, so a space is not a reason.
//
// A written response means failure; the caller returns.
func (s *server) deleteJustification(w http.ResponseWriter, r *http.Request, entity string) (string, bool) {
	var req model.DeleteInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return "", false
	}
	justification := strings.TrimSpace(req.Justification)
	if justification == "" && s.requiresJustification() {
		s.observeDelete(entity, opDelete, deleteJustificationRequired)
		writeErr(w, http.StatusUnprocessableEntity, justificationRequiredMsg)
		return "", false
	}
	return justification, true
}

// deleteTask handles DELETE /api/v1/tasks/{id}: tombstone the task, closing
// any active lease in the same transaction (store.DeleteTask). Answers with
// the task, whose "tombstone" now carries the actor, the time and the reason.
func (s *server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	justification, ok := s.deleteJustification(w, r, entityTask)
	if !ok {
		return
	}
	actorID := actorIDFrom(r)

	err := s.recordEvent(r.Context(), "cli", "task.deleted",
		map[string]string{"task": id, "actor": actorID, "justification": justification},
		func(tx *sql.Tx, eventID int64) error {
			return store.DeleteTask(tx, s.st.Now(), id, actorID, justification, eventID)
		})
	s.observeDelete(entityTask, opDelete, deleteOutcome(err))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.writeTask(w, r, id)
}

// undeleteTask handles POST /api/v1/tasks/{id}/undelete. No body and no
// justification on either instance (044 §3): deleting hides the record,
// undeleting restores it, and only the first is worth making someone stop and
// type.
func (s *server) undeleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// The write itself lives in deleted.go, shared with the cockpit's Restore
	// button, so the two surfaces differ only in the event source.
	err := s.recordUndeleteTask(r.Context(), "cli", id, actorIDFrom(r))
	s.observeDelete(entityTask, opUndelete, deleteOutcome(err))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.writeTask(w, r, id)
}

// writeTask reads a task back after its tombstone changed and answers with it.
// GetTask does not filter tombstoned rows: fetching by id is the one path that
// still resolves a deleted row, and rendering its tombstone is what makes the
// mistake recoverable rather than mysterious (044 §4).
func (s *server) writeTask(w http.ResponseWriter, r *http.Request, id string) {
	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// deleteDoc handles DELETE /api/v1/docs/{id}: the document half of 044 §5. A
// document holds no lease, so the tombstone is the whole write.
func (s *server) deleteDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	justification, ok := s.deleteJustification(w, r, entityDoc)
	if !ok {
		return
	}
	actorID := actorIDFrom(r)
	now := s.st.Now()

	err := s.recordDocEvent(w, r, "delete", "doc.deleted", id,
		model.DeleteInput{Justification: justification},
		func(tx *sql.Tx, eventID int64) error {
			return store.DeleteDoc(tx, now, id, actorID, justification, eventID)
		})
	s.observeDelete(entityDoc, opDelete, deleteOutcome(err))
	if err != nil {
		// recordDocEvent already wrote the response.
		return
	}
	s.writeDoc(w, r, id)
}

// undeleteDoc handles POST /api/v1/docs/{id}/undelete. See undeleteTask.
func (s *server) undeleteDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	// Shared with the cockpit's Restore button (deleted.go), which is why the
	// error comes back rather than being written here: this handler owes a
	// JSON body and that one owes an HTML page.
	err := s.recordUndeleteDoc(r.Context(), docSource, id, actorIDFrom(r))
	s.observeDelete(entityDoc, opUndelete, deleteOutcome(err))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.writeDoc(w, r, id)
}

// writeDoc reads a document back after its tombstone changed and answers with
// it. Like writeTask, the by-id read still resolves a tombstoned row.
func (s *server) writeDoc(w http.ResponseWriter, r *http.Request, id int64) {
	d, err := s.st.GetDoc(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// deleteOutcome classifies a delete or undelete for the
// worklode_deletes_total outcome label. An already-deleted row
// (store.ErrInvalidInput) lands in "error" along with everything else that is
// not a missing row: the four values are 044 §6's, and refusing to widen them
// keeps the one worth a dashboard — justification_required — legible.
func deleteOutcome(err error) string {
	switch {
	case err == nil:
		return deleteOK
	case errors.Is(err, store.ErrNotFound):
		return deleteNotFound
	default:
		return deleteError
	}
}
