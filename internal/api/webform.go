// webform.go makes the cockpit writable: the two creation forms (a new task,
// a declared deliverable) and the POST handlers behind them. Everything else
// in the web UI is a projection; these are the only routes that write.
//
// Three properties hold for every handler here:
//
//   - They write through the same code the JSON API writes through
//     (store.CreateTask via RecordEvent, recordDeliverable), so a task typed
//     into a browser and one posted by the CLI are indistinguishable
//     afterwards except for the event source, which is "web" precisely so
//     that "who typed this" stays answerable.
//   - They are POST-redirect-GET. A rejected submit re-renders the form with
//     the message and everything the person typed; a successful one 303s to
//     the created object, so a reload never creates a second one.
//   - They accept only same-origin submissions (see sameOriginForm). The
//     session cookie is already SameSite=Lax, which keeps a cross-site POST
//     from carrying it; the header check is the second lock, and the one that
//     still holds in a deployment with no login provider configured, where the
//     subject is the anonymous authOpen one and there is no cookie to withhold.
//
// Both routes carry permWebWrite (routeGuards), so reaching them at all is a
// policy decision made in authz.go, not something these handlers re-check.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// maxWebForm caps a web form submission at 256 KiB — generous for a task body
// typed by a person, small enough that an unauthenticated open deployment
// cannot be made to buffer megabytes per request.
const maxWebForm = 256 << 10

// webTaskKinds are the kinds the new-task form offers, in menu order. It
// omits "epic": the API still accepts one (validKinds is the authority on
// what the server takes), but specs 025 §10 and 029 §2 retire the kind, so a
// new surface must not hand people a way to mint more.
var webTaskKinds = []string{"feature", "bug", "chore", "spec", "review", "spike"}

// webTaskPriorities are the priorities the new-task form offers, most urgent
// first, mirroring validPriorities.
var webTaskPriorities = []string{"critical", "high", "medium", "low"}

// webTaskConcerns are the optional concerns the new-task form offers,
// mirroring store's validConcerns. The empty value is rendered as "None".
var webTaskConcerns = []string{"completeness", "performance", "usability", "security"}

// taskFormValues are the new-task form's fields as submitted, kept whole so a
// rejected submit re-renders exactly what the person typed.
type taskFormValues struct {
	Title    string
	Body     string
	Priority string
	Kind     string
	Concern  string
	Draft    bool
}

// deliverableFormValues are the deliverable form's fields as submitted.
type deliverableFormValues struct {
	Name        string
	Description string
	URL         string
}

// sameOriginForm reports whether a state-changing form submission came from
// this application's own pages. Sec-Fetch-Site is authoritative where the
// browser sends it ("none" is a direct navigation, which a form POST is not,
// but curl and the e2e harness send neither header and must still work);
// otherwise an Origin, if present, must match the request host or the
// configured public URL.
func (s *server) sameOriginForm(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "same-site", "cross-site":
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if s.cfg.PublicURL != "" {
		if pub, err := url.Parse(s.cfg.PublicURL); err == nil && pub.Host != "" {
			return strings.EqualFold(u.Host, pub.Host)
		}
	}
	return false
}

// webActor returns the actor id to attribute a form write to, or "" when
// there is none: a deployment with no login provider configured, where the
// subject is permitted but anonymous (authOpen). webGuard already resolved
// and validated the subject — including confirming the actor row still
// exists — so this is a context read, not a second authentication.
func (s *server) webActor(r *http.Request) string {
	return subjectFrom(r).ActorID
}

// projectHeader loads the project identity the project-scoped shell needs
// (name and key beside the local navigation). ErrNotFound propagates, so an
// unknown project 404s the same way every other project route does.
func (s *server) projectHeader(ctx context.Context, id string) (ui.CockpitProject, error) {
	p, err := s.st.GetProject(ctx, id)
	if err != nil {
		return ui.CockpitProject{}, err
	}
	return ui.CockpitProject{ID: p.ID, Name: p.Name, Key: p.Key}, nil
}

// parseWebForm caps and parses a form body. A body over the cap or a
// malformed encoding is a 400 — there is no form to re-render values into
// when the values could not be read.
func parseWebForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxWebForm)
	if err := r.ParseForm(); err != nil {
		webErr(w, http.StatusBadRequest, "could not read the submitted form")
		return false
	}
	return true
}

// renderWeb writes one rendered page with the given status. Form pages use it
// for the 422 re-render; the plain GET pages go through it too, so the
// content type is set in one place.
func (s *server) renderWeb(w http.ResponseWriter, r *http.Request, status int, page string, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		s.log.Error("render "+page, "err", err)
	}
}

// --- new task ---------------------------------------------------------------

// newTaskPage handles GET /projects/{id}/tasks/new: the empty form, with the
// defaults a task gets when nothing is chosen.
func (s *server) newTaskPage(w http.ResponseWriter, r *http.Request) {
	project, err := s.projectHeader(r.Context(), r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	values := taskFormValues{Priority: "medium", Kind: "feature"}
	s.renderWeb(w, r, http.StatusOK, "new task page", ui.NewTask(newTaskView(project, values, "")))
}

// createTaskFromForm handles POST /projects/{id}/tasks: create the task and
// 303 to it, or re-render the form at 422 with the one thing to fix.
func (s *server) createTaskFromForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.sameOriginForm(r) {
		s.observeFormSubmission("task", "forbidden")
		webErr(w, http.StatusForbidden, "cross-origin form submissions are not accepted")
		return
	}
	project, err := s.projectHeader(ctx, r.PathValue("id"))
	if err != nil {
		s.observeFormSubmission("task", formOutcome(err))
		s.webStoreErr(w, err)
		return
	}
	if !parseWebForm(w, r) {
		s.observeFormSubmission("task", "invalid")
		return
	}

	values := taskFormValues{
		Title:    strings.TrimSpace(r.PostFormValue("title")),
		Body:     strings.TrimSpace(r.PostFormValue("body")),
		Priority: r.PostFormValue("priority"),
		Kind:     r.PostFormValue("kind"),
		Concern:  r.PostFormValue("concern"),
		Draft:    r.PostFormValue("draft") != "",
	}
	if msg := validateTaskForm(&values); msg != "" {
		s.observeFormSubmission("task", "invalid")
		s.renderWeb(w, r, http.StatusUnprocessableEntity, "new task page",
			ui.NewTask(newTaskView(project, values, msg)))
		return
	}

	created, err := s.recordFormTask(ctx, project.ID, values, s.webActor(r))
	if err != nil {
		s.observeFormSubmission("task", formOutcome(err))
		s.webStoreErr(w, err)
		return
	}
	s.observeFormSubmission("task", "created")
	http.Redirect(w, r, "/tasks/"+created.ID, http.StatusSeeOther)
}

// validateTaskForm checks the submitted task fields, normalizing the two
// choices to their defaults when a browser sent nothing for them. It returns
// the one message to show, or "" when the form is good.
func validateTaskForm(v *taskFormValues) string {
	if v.Priority == "" {
		v.Priority = "medium"
	}
	if v.Kind == "" {
		v.Kind = "feature"
	}
	// The title is counted in runes, so the check and the field's HTML
	// maxlength agree about a title written in a non-Latin script; the body
	// bound is a byte budget on what lands in the database, not a writing
	// limit, so bytes are the right unit there.
	switch {
	case v.Title == "":
		return "A title is required."
	case utf8.RuneCountInString(v.Title) > maxTaskTitle:
		return "The title is too long (200 characters at most)."
	case len(v.Body) > maxTaskBody:
		return "The body is too long."
	case !validPriorities[v.Priority]:
		return "Choose a priority of critical, high, medium, or low."
	case !validKinds[v.Kind]:
		return "Choose one of the offered kinds."
	case v.Concern != "" && !store.ValidConcern(v.Concern):
		return "Choose a concern of completeness, performance, usability, or security — or none."
	}
	return ""
}

// maxTaskTitle matches the title field's HTML maxlength; maxTaskBody caps a
// form-submitted body. Bodies carry design work (spec 021), so the limit is
// generous — it exists to keep a runaway paste out of the database, not to
// shape what people write.
const (
	maxTaskTitle = 200
	maxTaskBody  = 64 << 10
)

// recordFormTask writes the task through the same RecordEvent + CreateTask
// path POST /api/v1/tasks uses, under the "web" event source.
func (s *server) recordFormTask(ctx context.Context, projectID string, v taskFormValues, actorID string) (*store.Task, error) {
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"project": projectID, "title": v.Title, "body": v.Body,
		"priority": v.Priority, "kind": v.Kind, "concern": v.Concern,
		"draft": v.Draft, "created_by": actorID,
	})
	if err != nil {
		return nil, err
	}
	now := s.st.Now()

	var created *store.Task
	if _, _, err := s.st.RecordEvent(ctx, "web", extID, "task.created", payload,
		func(tx *sql.Tx, eventID int64) error {
			t, err := store.CreateTask(tx, now, store.TaskInput{
				ProjectID: projectID,
				Title:     v.Title,
				Body:      v.Body,
				Priority:  v.Priority,
				Kind:      v.Kind,
				Concern:   v.Concern,
				CreatedBy: actorID,
				Draft:     v.Draft,
			})
			if err != nil {
				return err
			}
			created = t
			return nil
		}); err != nil {
		return nil, err
	}
	return created, nil
}

// --- deliverables -----------------------------------------------------------

// deliverablesPage handles GET /projects/{id}/deliverables: the project's
// declared deliverables and the affordance to declare another. It replaces
// the honest placeholder this destination used to render.
func (s *server) deliverablesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.projectHeader(ctx, r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	items, err := s.st.ListDeliverables(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	s.renderWeb(w, r, http.StatusOK, "deliverables page", ui.Deliverables(deliverablesView(project, items)))
}

// newDeliverablePage handles GET /projects/{id}/deliverables/new.
func (s *server) newDeliverablePage(w http.ResponseWriter, r *http.Request) {
	project, err := s.projectHeader(r.Context(), r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	s.renderWeb(w, r, http.StatusOK, "new deliverable page",
		ui.NewDeliverable(newDeliverableView(project, deliverableFormValues{}, "")))
}

// createDeliverableFromForm handles POST /projects/{id}/deliverables: declare
// the deliverable and 303 back to the list, or re-render at 422.
func (s *server) createDeliverableFromForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.sameOriginForm(r) {
		s.observeFormSubmission("deliverable", "forbidden")
		webErr(w, http.StatusForbidden, "cross-origin form submissions are not accepted")
		return
	}
	project, err := s.projectHeader(ctx, r.PathValue("id"))
	if err != nil {
		s.observeFormSubmission("deliverable", formOutcome(err))
		s.webStoreErr(w, err)
		return
	}
	if !parseWebForm(w, r) {
		s.observeFormSubmission("deliverable", "invalid")
		return
	}

	values := deliverableFormValues{
		Name:        strings.TrimSpace(r.PostFormValue("name")),
		Description: strings.TrimSpace(r.PostFormValue("description")),
		URL:         strings.TrimSpace(r.PostFormValue("url")),
	}
	in, msg := validateDeliverable(project.ID, values.Name, values.Description, values.URL, s.webActor(r))
	if msg != "" {
		s.observeFormSubmission("deliverable", "invalid")
		s.renderWeb(w, r, http.StatusUnprocessableEntity, "new deliverable page",
			ui.NewDeliverable(newDeliverableView(project, values, formMessage(msg))))
		return
	}

	if _, err := s.recordDeliverable(ctx, "web", in); err != nil {
		s.observeFormSubmission("deliverable", formOutcome(err))
		s.webStoreErr(w, err)
		return
	}
	s.observeFormSubmission("deliverable", "created")
	http.Redirect(w, r, "/projects/"+project.ID+"/deliverables", http.StatusSeeOther)
}

// formMessage turns a JSON-API validation message ("name is required") into
// the sentence the form shows. The API's wording is terse by convention; a
// person reading a form is owed a sentence.
func formMessage(msg string) string {
	switch msg {
	case "name is required":
		return "A name is required."
	case "url must be an absolute http or https address":
		return "The URL must be an absolute http:// or https:// address."
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "."
}
