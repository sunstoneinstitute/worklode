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
