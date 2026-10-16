package store

import (
	"context"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// SummaryTask is one live task as the project summary reads it.
type SummaryTask struct {
	ID, Title, State, Assignee, Due string
	Closed                          bool // taskClosed verdict (WL-REQ-87)
}

// SummaryDoc is a document whose current version still owes a review.
type SummaryDoc struct{ Ref, Title, URL string }

// SummaryFacts is the raw input of the project summary (WL-REQ-1797); the
// grouping and counting happen in the reader above the store.
type SummaryFacts struct {
	Tasks  []SummaryTask
	Roster []Participant
	Docs   []SummaryDoc
}

// ProjectSummaryFacts reads the facts for projectID's summary. ErrNotFound for
// an unknown project.
func (s *Store) ProjectSummaryFacts(ctx context.Context, projectID string) (*SummaryFacts, error) {
	roster, err := s.ListParticipants(ctx, projectID) // ErrNotFound on unknown project
	if err != nil {
		return nil, err
	}
	tasks, err := s.ListTasks(ctx, TaskFilter{Project: projectID})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	closed, err := s.ClosedTaskIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	f := &SummaryFacts{Roster: roster}
	for _, t := range tasks {
		f.Tasks = append(f.Tasks, SummaryTask{t.ID, t.Title, t.State, t.Assignee, t.Due, closed[t.ID]})
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT d.id FROM docs d
		  WHERE d.project_id = $1 AND d.deleted_at IS NULL
		    AND EXISTS (SELECT 1 FROM approvals a
		                 WHERE a.entity_kind = 'doc' AND a.entity_id = 'doc:' || d.id
		                   AND a.state IN ('awaiting', 'changes_requested')
		                   AND a.required_actor IS NOT NULL)`, projectID)
	if err != nil {
		return nil, fmt.Errorf("summary docs for %s: %w", projectID, err)
	}
	docIDs, err := collectRows(rows, "summary docs", func(r rowScanner) (id int64, err error) {
		err = r.Scan(&id)
		return
	})
	if err != nil {
		return nil, err
	}
	for _, id := range docIDs {
		d, err := s.GetDoc(ctx, id)
		if err != nil {
			return nil, err
		}
		f.Docs = append(f.Docs, SummaryDoc{d.FormatRef(), d.Title, model.DocCanonicalURL(*d)})
	}
	return f, nil
}
