// deleted.go serves the cockpit's Deleted destination: the project-local page
// listing the tasks and design documents spec 044 tombstoned, and the per-row
// Restore button that undeletes one.
//
// Spec 044 ships delete and undelete on the JSON API and the CLI, and every
// cockpit page reads through the same `deleted_at IS NULL` store calls the
// CLI does — so a deleted row correctly vanishes from all of them, and until
// this page there was nowhere in a browser to see that it had. That matters
// on a prod instance, where a delete is refused without a justification (044
// §3) precisely so someone can review it later.
//
// The page reuses the store's existing `--deleted` filters (TaskFilter and
// DocFilter's Deleted switch), so it lists exactly what `lode task list
// --deleted` and `lode doc list --deleted` list, narrowed to one project.
//
// Restore is two routes, not one: undeleting a task is permTaskWrite and
// undeleting a document is permDocWrite (044 §5), routeGuards names one
// permission per route, and collapsing the two into a single endpoint would
// mean one of the halves ran under the other's authority.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// deletedPage handles GET /projects/{id}/deleted. It loads the project header
// first (so an unknown project 404s the way every other project route does),
// then both tombstone lists.
func (s *server) deletedPage(w http.ResponseWriter, r *http.Request) {
	project, err := s.projectHeader(r.Context(), r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	v, err := s.deletedView(r.Context(), project)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	s.renderWeb(w, r, http.StatusOK, "deleted page", ui.Deleted(v))
}

// deletedView reads a project's tombstoned tasks and documents and maps them
// into the page's view type.
func (s *server) deletedView(ctx context.Context, project ui.CockpitProject) (ui.DeletedView, error) {
	tasks, err := s.st.ListTasks(ctx, store.TaskFilter{Project: project.ID, Deleted: true})
	if err != nil {
		return ui.DeletedView{}, err
	}
	docs, err := s.st.ListDocs(ctx, store.DocFilter{Project: project.ID, Deleted: true})
	if err != nil {
		return ui.DeletedView{}, err
	}
	v := ui.DeletedView{
		Page:              ui.PageProps{Title: "worklode: " + project.Name + ": Deleted"},
		CanonicalURL:      "/projects/" + project.ID + "/deleted",
		Project:           project,
		Tasks:             tasks,
		Docs:              make([]ui.DeletedDocRow, 0, len(docs)),
		RestoreTaskAction: "/projects/" + project.ID + "/deleted/tasks/restore",
		RestoreDocAction:  "/projects/" + project.ID + "/deleted/docs/restore",
	}
	// Bodies are dropped for the reason docsView states: the page renders
	// none of the markdown, and carrying every tombstoned document's source
	// into it would make this the heaviest page the cockpit serves.
	for _, d := range withoutDocBodies(docs) {
		v.Docs = append(v.Docs, ui.DeletedDocRow{Doc: d, URL: docPageURL(d.ID), Ref: docRef(d)})
	}
	return v, nil
}

// restoreTaskFromForm handles POST /projects/{id}/deleted/tasks/restore, the
// per-row Restore button on a task. A refused restore re-renders the page
// with the reason; a successful one 303s back to it, so a reload never
// restores twice — the second undelete would be refused as "not deleted"
// anyway, but a redirect is the honest answer rather than an error page.
func (s *server) restoreTaskFromForm(w http.ResponseWriter, r *http.Request) {
	project, ok := s.beginFormPost(w, r, formRestoreTask)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.PostFormValue("task"))
	if id == "" {
		s.observeFormSubmission(formRestoreTask, "invalid")
		s.renderRestoreRefusal(w, r, project, "That restore named no task.")
		return
	}

	err := s.recordUndeleteTask(r.Context(), "web", id, actorIDFrom(r))
	s.observeDelete(entityTask, opUndelete, deleteOutcome(err))
	if err != nil {
		s.observeFormSubmission(formRestoreTask, formOutcome(err))
		s.restoreErr(w, r, project, id, err)
		return
	}
	s.observeFormSubmission(formRestoreTask, "created")
	http.Redirect(w, r, "/projects/"+project.ID+"/deleted", http.StatusSeeOther)
}

// restoreDocFromForm handles POST /projects/{id}/deleted/docs/restore, the
// document half of restoreTaskFromForm.
func (s *server) restoreDocFromForm(w http.ResponseWriter, r *http.Request) {
	project, ok := s.beginFormPost(w, r, formRestoreDoc)
	if !ok {
		return
	}
	raw := strings.TrimSpace(r.PostFormValue("doc"))
	id, convErr := strconv.ParseInt(raw, 10, 64)
	if raw == "" || convErr != nil {
		s.observeFormSubmission(formRestoreDoc, "invalid")
		s.renderRestoreRefusal(w, r, project, "That restore named no document.")
		return
	}

	err := s.recordUndeleteDoc(r.Context(), "web", id, actorIDFrom(r))
	s.observeDelete(entityDoc, opUndelete, deleteOutcome(err))
	if err != nil {
		s.observeFormSubmission(formRestoreDoc, formOutcome(err))
		s.restoreErr(w, r, project, raw, err)
		return
	}
	s.observeFormSubmission(formRestoreDoc, "created")
	http.Redirect(w, r, "/projects/"+project.ID+"/deleted", http.StatusSeeOther)
}

// restoreErr turns a refused restore into the page the person sees. A row
// that is gone or is no longer deleted — someone else restored it, the id was
// edited — belongs back on the list with the reason, since re-reading the
// list is what answers it. Only a genuine fault falls through to an error
// page.
func (s *server) restoreErr(w http.ResponseWriter, r *http.Request,
	project ui.CockpitProject, ref string, cause error) {
	switch {
	case errors.Is(cause, store.ErrNotFound):
		s.renderRestoreRefusal(w, r, project, "There is no "+ref+" to restore.")
	case errors.Is(cause, store.ErrInvalidInput):
		s.renderRestoreRefusal(w, r, project, ref+" is not deleted. It may already have been restored.")
	default:
		s.webStoreErr(w, cause)
	}
}

// renderRestoreRefusal re-renders the Deleted page at 422 with one message.
// The lists are re-read rather than reused, so the page shows the tombstones
// as they are now — which is the answer to every refusal it reports.
func (s *server) renderRestoreRefusal(w http.ResponseWriter, r *http.Request,
	project ui.CockpitProject, msg string) {
	v, err := s.deletedView(r.Context(), project)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	v.RestoreError = msg
	s.renderWeb(w, r, http.StatusUnprocessableEntity, "deleted page", ui.Deleted(v))
}

// Form names for worklode_web_form_submissions_total. Bounded label values,
// one per POST handler, matching the "task"/"deliverable"/"crew_add" naming
// the other forms use.
const (
	formRestoreTask = "task_restore"
	formRestoreDoc  = "doc_restore"
)

// recordUndeleteTask clears a task's tombstone through the event log. source
// names the surface — "cli" for DELETE's undelete route, "web" for the
// cockpit's Restore button — which is the only difference between the two
// paths, so a task restored in a browser and one restored by the CLI are the
// same write.
func (s *server) recordUndeleteTask(ctx context.Context, source, id, actorID string) error {
	return s.recordEvent(ctx, source, "task.undeleted",
		map[string]string{"task": id, "actor": actorID},
		func(tx *sql.Tx, eventID int64) error {
			return store.UndeleteTask(tx, s.st.Now(), id, eventID)
		})
}

// recordUndeleteDoc is recordUndeleteTask for a document. It goes through
// RecordDocEvent directly rather than through recordDocEvent, which writes a
// JSON error response of its own: the cockpit owes a refused restore an HTML
// page, so the error comes back here and the caller decides how to render it.
// The payload shape is recordDocEvent's, so the two surfaces log the same
// event.
func (s *server) recordUndeleteDoc(ctx context.Context, source string, id int64, actorID string) error {
	extID, err := randomExternalID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"doc": id, "actor": actorID, "request": nil,
	})
	if err != nil {
		return err
	}
	_, _, err = s.st.RecordDocEvent(ctx, "undelete", source, extID, "doc.undeleted", payload,
		func(tx *sql.Tx, eventID int64) error {
			return store.UndeleteDoc(tx, s.st.Now(), id, eventID)
		})
	return err
}
