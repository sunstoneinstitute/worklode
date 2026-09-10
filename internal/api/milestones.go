// milestones.go serves spec 029 §2's milestone over the JSON API: one
// ordered container in a project, holding tasks and deliverables. There is no
// cockpit form for it — the cockpit stays read-mostly here, and the promotion
// transaction is what mints a project's default set in bulk.
package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// recordMilestone writes one milestone through RecordEvent, so the event log
// carries the fact and the source names the surface it came from ("cli" for
// the JSON API), matching deliverables.go's recordDeliverable.
//
// The payload spelling is pinned by the milestones plan:
// {project, id, title, position, created_by}. Three of those are only known
// once the transaction has run — the minted id, the trimmed title, and a
// position 0 resolved against the project's existing milestones — so they are
// merged in from apply the way task.created merges its task id.
func (s *server) recordMilestone(ctx context.Context, source, projectID, title string, position int, createdBy string) (*model.Milestone, error) {
	now := s.st.Now()

	var created *model.Milestone
	if err := s.recordEvent(ctx, source, "milestone.created", map[string]string{
		"project":    projectID,
		"created_by": createdBy,
	}, func(tx *sql.Tx, eventID int64) error {
		m, err := store.CreateMilestone(tx, now, projectID, title, position, createdBy)
		if err != nil {
			return err
		}
		if err := store.MergeEventPayload(tx, eventID, map[string]any{
			"id":       m.ID,
			"title":    m.Title,
			"position": strconv.Itoa(m.Position),
		}); err != nil {
			return err
		}
		created = m
		return nil
	}); err != nil {
		return nil, err
	}
	return created, nil
}

// listProjectMilestones handles GET /api/v1/projects/{id}/milestones. It
// loads the project first, so an unknown project 404s the way
// listProjectDeliverables' does rather than returning an empty list for a
// project that was never there.
func (s *server) listProjectMilestones(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(r.Context(), projectID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	items, err := s.st.ListMilestones(r.Context(), projectID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.MilestoneListResponse{Milestones: items})
}

// getMilestone handles GET /api/v1/milestones/{id}.
func (s *server) getMilestone(w http.ResponseWriter, r *http.Request) {
	d, err := s.st.GetMilestone(r.Context(), r.PathValue("id"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// createMilestone handles POST /api/v1/projects/{id}/milestones. Validation
// lives in store.CreateMilestone so no caller can drift into accepting a
// different milestone; mapStoreErr turns its refusals into 422 (bad title or
// position) and 404 (unknown project).
func (s *server) createMilestone(w http.ResponseWriter, r *http.Request) {
	var req model.CreateMilestoneInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	created, err := s.recordMilestone(r.Context(), "cli", r.PathValue("id"),
		req.Title, req.Position, actorIDFrom(r))
	s.observeMilestoneChange("create", err)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// milestonesPage handles GET /projects/{id}/milestones, the project-local
// Milestones destination (spec 029 §2, spec 032 §10): every milestone as a
// section, in position order, with the children its progress was derived
// from. It loads the project header first, so an unknown project 404s the
// same way every other project route does.
func (s *server) milestonesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.projectHeader(ctx, r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	v, err := s.milestonesPageView(ctx, project)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	s.renderWeb(w, r, http.StatusOK, "milestones page", ui.Milestones(v))
}

// milestonesPageView reads everything the page renders. Both the GET and the
// re-render a refused reference submit needs go through it, so the two views
// cannot drift.
func (s *server) milestonesPageView(ctx context.Context, project ui.CockpitProject) (ui.MilestonesView, error) {
	milestones, err := s.st.ListMilestones(ctx, project.ID)
	if err != nil {
		return ui.MilestonesView{}, err
	}
	tasks, deliverables, err := s.st.ListMilestoneChildren(ctx, project.ID)
	if err != nil {
		return ui.MilestonesView{}, err
	}
	// One read per milestone: references cross project boundaries (029 §5),
	// so they are not in ListMilestoneChildren's project-scoped result.
	// ponytail: a project holds a handful of milestones; batch it if that
	// stops being true.
	refs := make(map[string][]model.Deliverable, len(milestones))
	for _, m := range milestones {
		got, err := s.st.MilestoneDeliverableRefs(ctx, m.ID)
		if err != nil {
			return ui.MilestonesView{}, err
		}
		refs[m.ID] = got
	}
	return milestonesView(project, milestones, tasks, deliverables, refs), nil
}

// milestonesView maps a project's milestones and their children into the
// Milestones page. The counts come off the list reader's derived progress —
// the page repeats the numbers the store derived, and never re-derives them
// from the rows it happens to be rendering.
func milestonesView(project ui.CockpitProject, milestones []model.Milestone,
	tasks map[string][]model.Task, deliverables map[string][]model.Deliverable,
	refs map[string][]model.Deliverable) ui.MilestonesView {
	v := ui.MilestonesView{
		Page:         ui.PageProps{Title: "worklode: " + project.Name + ": Milestones"},
		CanonicalURL: "/projects/" + project.ID + "/milestones",
		Project:      project,
		Milestones:   make([]ui.MilestoneSection, 0, len(milestones)),
	}
	for _, m := range milestones {
		section := ui.MilestoneSection{
			ID:                m.ID,
			AddAction:         "/projects/" + project.ID + "/milestones/" + m.ID + "/references",
			Title:             m.Title,
			TasksTotal:        m.Progress.TasksTotal,
			TasksClosed:       m.Progress.TasksClosed,
			DeliverablesTotal: m.Progress.DeliverablesTotal,
			DeliverablesLive:  m.Progress.DeliverablesLive,
		}
		for _, t := range tasks[m.ID] {
			section.Tasks = append(section.Tasks, ui.MilestoneTaskRow{
				ID: t.ID, Title: t.Title, State: t.State, Assignee: t.Assignee,
			})
		}
		for _, d := range deliverables[m.ID] {
			section.Deliverables = append(section.Deliverables, ui.DeliverableRow{
				ID:            d.ID,
				Name:          d.Name,
				Description:   d.Description,
				URL:           d.URL,
				CreatedBy:     d.CreatedBy,
				CreatedAt:     d.CreatedAt,
				Artifact:      d.Artifact,
				Label:         d.Label,
				ReportedState: d.ReportedState,
				ReportedAt:    d.ReportedAt,
			})
		}
		for _, d := range refs[m.ID] {
			section.References = append(section.References, ui.MilestoneRefRow{
				ID: d.ID, Project: d.Project, Name: d.Name, State: d.ReportedState,
			})
		}
		v.Milestones = append(v.Milestones, section)
	}
	return v
}

// formMilestoneRef is the worklode_web_form_submissions_total form label for
// the milestone page's add-a-reference form.
const formMilestoneRef = "milestone_reference"

// addMilestoneReferenceFromForm handles POST
// /projects/{id}/milestones/{mid}/references, the milestones page's own add
// affordance. It writes through the same recordReference path POST
// /api/v1/references writes through, under the "web" event source, so a
// reference typed into a browser and one posted by the CLI differ only in
// which surface the event log names. A refused add re-renders the page with
// the message on the milestone it was typed into; a good one 303s back, so a
// reload never adds a second edge.
func (s *server) addMilestoneReferenceFromForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, ok := s.beginFormPost(w, r, formMilestoneRef)
	if !ok {
		return
	}
	milestoneID := r.PathValue("mid")
	typed := strings.TrimSpace(r.PostFormValue("deliverable"))

	err := errMissingDeliverable
	if typed != "" {
		_, err = s.recordReference(ctx, "web", store.CreateEntityEdgeInput{
			FromKind: "milestone", From: milestoneID,
			ToKind: "deliverable", To: typed,
			Rel:       "depends_on",
			CreatedBy: actorIDFrom(r),
		})
	}
	if err != nil {
		s.observeFormSubmission(formMilestoneRef, formOutcome(err))
		// A refused add is about what was typed — an unknown id, an id that
		// is already referenced — so it belongs back on the form. Only a
		// genuine fault falls through to the error page.
		if !errors.Is(err, store.ErrInvalidInput) && !errors.Is(err, store.ErrNotFound) &&
			!errors.Is(err, store.ErrReferenceExists) {
			s.webStoreErr(w, err)
			return
		}
		v, viewErr := s.milestonesPageView(ctx, project)
		if viewErr != nil {
			s.webStoreErr(w, viewErr)
			return
		}
		for i := range v.Milestones {
			if v.Milestones[i].ID == milestoneID {
				v.Milestones[i].AddValue = typed
				v.Milestones[i].AddError = referenceFormMessage(err)
			}
		}
		s.renderWeb(w, r, http.StatusUnprocessableEntity, "milestones page", ui.Milestones(v))
		return
	}
	s.observeFormSubmission(formMilestoneRef, "created")
	http.Redirect(w, r, "/projects/"+project.ID+"/milestones", http.StatusSeeOther)
}

// errMissingDeliverable stands in for the store refusal an empty field would
// have produced, so the empty case and the unknown-id case take one path.
var errMissingDeliverable = fmt.Errorf("a deliverable id is required: %w", store.ErrInvalidInput)

// referenceFormMessage turns a refused add into the sentence the form shows.
// An unknown id and a duplicate both name what to do about them; anything
// else falls back to the store's own message, which is what the person has
// to act on.
func referenceFormMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "No deliverable with that id. Check the id, or declare it first."
	case errors.Is(err, store.ErrReferenceExists):
		return "This milestone already references that deliverable."
	}
	return formMessage(strings.TrimSuffix(err.Error(), ": "+store.ErrInvalidInput.Error()))
}
