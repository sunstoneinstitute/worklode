package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// AppendTaskActivity inserts rows in one statement, skipping any row whose
// task id does not exist (a stale stamp on an already-deleted or unknown
// task must not fail the whole batch, spec 071 §2). Returns the number of
// rows actually inserted. An empty slice issues no query.
//
// WITH ORDINALITY plus ORDER BY is what makes the ids follow the batch
// order. Readers page this table by id, so without it one export could show
// a tool result ahead of the decision that produced it.
func (s *Store) AppendTaskActivity(ctx context.Context, rows []model.TaskActivity) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	taskIDs := make([]string, len(rows))
	actorIDs := make([]string, len(rows))
	agents := make([]string, len(rows))
	sessionIDs := make([]string, len(rows))
	ats := make([]time.Time, len(rows))
	events := make([]string, len(rows))
	attrs := make([]string, len(rows))
	for i, r := range rows {
		taskIDs[i] = r.Task
		actorIDs[i] = r.Actor
		agents[i] = r.Agent
		sessionIDs[i] = r.Session
		ats[i] = r.At.UTC()
		events[i] = r.Event
		b, err := json.Marshal(r.Attrs)
		if err != nil {
			return 0, fmt.Errorf("marshal task activity attrs: %w", err)
		}
		attrs[i] = string(b)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO task_activity (task_id, actor_id, agent, session_id, at, event, attrs)
		 SELECT r.task_id, r.actor_id, r.agent, r.session_id, r.at, r.event, r.attrs
		   FROM unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::timestamptz[], $6::text[], $7::jsonb[])
		        WITH ORDINALITY AS r(task_id, actor_id, agent, session_id, at, event, attrs, ord)
		  WHERE EXISTS (SELECT 1 FROM tasks WHERE id = r.task_id)
		  ORDER BY r.ord`,
		taskIDs, actorIDs, agents, sessionIDs, ats, events, attrs,
	)
	if err != nil {
		return 0, fmt.Errorf("append task activity: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("append task activity: rows affected: %w", err)
	}
	return int(n), nil
}

// taskActivityColumns is the SELECT list scanTaskActivity expects, in order.
const taskActivityColumns = `id, task_id, actor_id, agent, session_id, at, event, attrs`

// TaskActivity returns a page of one task's activity log, ordered by the
// cursor semantics spec 071 §2 defines: after == 0 is the newest limit rows,
// newest first, for the initial page; after > 0 is rows with id > after,
// oldest of that set first, for polling a live stream forward.
func (s *Store) TaskActivity(ctx context.Context, taskID string, after int64, limit int) ([]model.TaskActivity, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if after == 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+taskActivityColumns+` FROM task_activity
			  WHERE task_id = $1
			  ORDER BY id DESC LIMIT $2`,
			taskID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+taskActivityColumns+` FROM task_activity
			  WHERE task_id = $1 AND id > $2
			  ORDER BY id ASC LIMIT $3`,
			taskID, after, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list task activity for %s: %w", taskID, err)
	}
	return scanTaskActivity(rows)
}

func scanTaskActivity(rows *sql.Rows) ([]model.TaskActivity, error) {
	defer rows.Close()
	var out []model.TaskActivity
	for rows.Next() {
		var a model.TaskActivity
		var attrsRaw []byte
		if err := rows.Scan(&a.ID, &a.Task, &a.Actor, &a.Agent, &a.Session, &a.At, &a.Event, &attrsRaw); err != nil {
			return nil, fmt.Errorf("scan task activity: %w", err)
		}
		if len(attrsRaw) > 0 {
			if err := json.Unmarshal(attrsRaw, &a.Attrs); err != nil {
				return nil, fmt.Errorf("unmarshal task activity attrs: %w", err)
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PurgeTaskActivity deletes rows past their retention window (spec 071 §2):
// a closed task's rows older than 2 hours, or any task's rows older than 7
// days regardless of state (assumption A1 — an open task should not
// accumulate unbounded rows). Called as a third sweeper step alongside
// ExpireLeases and sweepStaleDocs.
func (s *Store) PurgeTaskActivity(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM task_activity a USING tasks t
		  WHERE t.id = a.task_id
		    AND ((`+taskClosed("t")+` AND a.at < $1::timestamptz - interval '2 hours')
		         OR a.at < $1::timestamptz - interval '7 days')`,
		now.UTC())
	if err != nil {
		return 0, fmt.Errorf("purge task activity: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge task activity: rows affected: %w", err)
	}
	return n, nil
}
