// milestones.go serves spec 029 §2's milestone over the JSON API: one
// ordered container in a project, holding tasks and deliverables. There is no
// cockpit form for it — the cockpit stays read-mostly here, and the promotion
// transaction is what mints a project's default set in bulk.
package api

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

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
		if err := store.MergeEventPayload(tx, eventID, map[string]string{
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
	milestones, err := s.st.ListMilestones(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	tasks, deliverables, err := s.st.ListMilestoneChildren(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	s.renderWeb(w, r, http.StatusOK, "milestones page",
		ui.Milestones(milestonesView(project, milestones, tasks, deliverables)))
}

// milestonesView maps a project's milestones and their children into the
// Milestones page. The counts come off the list reader's derived progress —
// the page repeats the numbers the store derived, and never re-derives them
// from the rows it happens to be rendering.
func milestonesView(project ui.CockpitProject, milestones []model.Milestone,
	tasks map[string][]model.Task, deliverables map[string][]model.Deliverable) ui.MilestonesView {
	v := ui.MilestonesView{
		Page:         ui.PageProps{Title: "worklode: " + project.Name + ": Milestones"},
		CanonicalURL: "/projects/" + project.ID + "/milestones",
		Project:      project,
		Milestones:   make([]ui.MilestoneSection, 0, len(milestones)),
	}
	for _, m := range milestones {
		section := ui.MilestoneSection{
			ID:                m.ID,
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
				ReportedState: d.ReportedState,
				ReportedAt:    d.ReportedAt,
			})
		}
		v.Milestones = append(v.Milestones, section)
	}
	return v
}
