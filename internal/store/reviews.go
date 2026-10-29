package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// OpenReview opens a review over a document (WL-REQ-1324). The roster is the
// document's doc_reviewers; each reviewer gets an awaiting approvals lane at
// the current version and one review task about the document (WL-REQ-1778).
// A second open review on the same document is ErrReviewOpen.
func OpenReview(tx *sql.Tx, now time.Time, in model.OpenReviewInput, actorID string, eventID int64) (*model.Review, error) {
	if in.Task != "" || in.Doc == 0 {
		return nil, fmt.Errorf("a review over a task's change comes with review surface part 2 (WL-PLAN-133); name a document: %w", ErrInvalidInput)
	}
	d, err := lockDoc(tx, in.Doc)
	if err != nil {
		return nil, err
	}
	if d.status == "superseded" {
		return nil, fmt.Errorf("doc %d is superseded and cannot be reviewed: %w", in.Doc, ErrInvalidInput)
	}
	reviewers, err := docReviewers(tx, in.Doc)
	if err != nil {
		return nil, err
	}
	if len(reviewers) == 0 {
		return nil, fmt.Errorf("doc %d has no reviewers; set reviewers with `lode doc set reviewers` first: %w", in.Doc, ErrInvalidInput)
	}
	ts := now.UTC().Truncate(time.Second)
	var id int64
	err = tx.QueryRow(
		`INSERT INTO reviews (doc_id, subject, state, base_ref, round, opened_by, opened_at)
		 VALUES ($1, 'doc', 'open', $2, 0, $3, $4) RETURNING id`,
		in.Doc, strconv.Itoa(d.version), actorID, ts).Scan(&id)
	if isUniqueViolationOn(err, "reviews_open_doc") {
		return nil, fmt.Errorf("doc %d: %w", in.Doc, ErrReviewOpen)
	}
	if err != nil {
		return nil, fmt.Errorf("open review of doc %d: %w", in.Doc, err)
	}
	if err := RequestDocApproval(tx, ts, in.Doc, d.version); err != nil {
		return nil, err
	}
	if err := assignReviewTasks(tx, ts, id, in.Doc, d.project, reviewers, actorID, eventID); err != nil {
		return nil, err
	}
	if err := recordReviewEvent(tx, ts, "wl:ReviewOpened", iri.CURIE(iri.Review(id)), actorID, eventID, nil); err != nil {
		return nil, err
	}
	return getReview(context.Background(), tx, id)
}

// assignReviewTasks gives each reviewer one open review task about docID
// (WL-REQ-1778). A reviewer who holds one keeps it; otherwise an unassigned
// draft or ready review task about the document (the doc-lifecycle
// subscriber's) is adopted, and only then is a new one minted.
func assignReviewTasks(tx *sql.Tx, now time.Time, reviewID, docID int64, project string, reviewers []string, actorID string, eventID int64) error {
	closed := slices.Collect(maps.Keys(deliveredStateSet))
	var ref string
	if err := tx.QueryRow(
		`SELECT p.key || '-' || upper(d.kind) || '-' || coalesce(d.number, 0)
		   FROM docs d JOIN projects p ON p.id = d.project_id WHERE d.id = $1`, docID).Scan(&ref); err != nil {
		return fmt.Errorf("read doc %d: %w", docID, err)
	}
	for _, r := range reviewers {
		var held bool
		if err := tx.QueryRow(
			`SELECT EXISTS (SELECT 1 FROM tasks WHERE about_doc = $1 AND kind = 'review'
			   AND assignee = $2 AND deleted_at IS NULL AND NOT (state = ANY($3)))`,
			docID, r, closed).Scan(&held); err != nil {
			return fmt.Errorf("read review tasks of doc %d: %w", docID, err)
		}
		if held {
			continue
		}
		var taskID string
		err := tx.QueryRow(
			`UPDATE tasks SET assignee = $1, updated_at = $3 WHERE id = (
			   SELECT id FROM tasks WHERE about_doc = $2 AND kind = 'review' AND assignee IS NULL
			      AND state IN ('draft', 'ready') AND deleted_at IS NULL
			    ORDER BY created_at, id LIMIT 1 FOR UPDATE)
			 RETURNING id`, r, docID, now).Scan(&taskID)
		if errors.Is(err, sql.ErrNoRows) {
			task, cerr := CreateTask(tx, now, TaskInput{
				ProjectID: project,
				Kind:      "review",
				Priority:  "medium",
				Title:     fmt.Sprintf("Review %s", ref),
				Body:      fmt.Sprintf("Review %d over %s is open. Read it with `lode review show %d`.\n", reviewID, ref, reviewID),
				AboutDoc:  docID,
				CreatedBy: actorID,
			}, eventID)
			if cerr != nil {
				return cerr
			}
			taskID = task.ID
			_, err = tx.Exec(`UPDATE tasks SET assignee = $1 WHERE id = $2`, r, taskID)
		}
		if err != nil {
			return fmt.Errorf("assign review task of doc %d to %s: %w", docID, r, err)
		}
		if err := LogChange(tx, "task", taskID, eventID,
			map[string]string{"field": "assignee", "old": "", "new": r}); err != nil {
			return err
		}
	}
	return nil
}

// reviewAuthor is the actor whose replies carry role author: a document's
// owner; for a change, the task's active lease holder, else its assignee,
// else "".
func reviewAuthor(tx *sql.Tx, r *model.Review) (string, error) {
	var author string
	var err error
	if r.Doc != 0 {
		err = tx.QueryRow(`SELECT coalesce(owner, '') FROM docs WHERE id = $1`, r.Doc).Scan(&author)
	} else {
		err = tx.QueryRow(
			`SELECT coalesce((SELECT actor_id FROM leases WHERE task_id = t.id AND released_at IS NULL), t.assignee, '')
			   FROM tasks t WHERE t.id = $1`, r.Task).Scan(&author)
	}
	if err != nil {
		return "", fmt.Errorf("read author of review %d: %w", r.ID, err)
	}
	return author, nil
}

const reviewColumns = `r.id, r.subject, coalesce(r.task_id, ''), coalesce(r.doc_id, 0), coalesce(r.rule_id, 0),
	r.state, r.base_ref, r.round, r.opened_by, r.opened_at, r.closed_at`

func scanReviewRow(row rowScanner) (*model.Review, error) {
	var r model.Review
	if err := row.Scan(&r.ID, &r.Subject, &r.Task, &r.Doc, &r.Rule,
		&r.State, &r.BaseRef, &r.Round, &r.OpenedBy, &r.OpenedAt, &r.ClosedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// reviewRoster is the review's reviewer set: doc_reviewers for a document,
// task_reviewers for a change, in assignment order.
func reviewRoster(ctx context.Context, db rowsQueryer, r *model.Review) ([]string, error) {
	q := `SELECT actor_id FROM doc_reviewers WHERE doc_id = $1 ORDER BY assigned_at, actor_id`
	var arg any = r.Doc
	if r.Subject == "change" {
		q = `SELECT actor_id FROM task_reviewers WHERE task_id = $1 ORDER BY assigned_at, actor_id`
		arg = r.Task
	}
	rows, err := db.QueryContext(ctx, q, arg)
	if err != nil {
		return nil, fmt.Errorf("load roster of review %d: %w", r.ID, err)
	}
	return collectRows(rows, fmt.Sprintf("load roster of review %d", r.ID), scanString)
}

// GetReview returns one review with its roster, threads (each with its
// messages) and rounds. A missing review is ErrNotFound.
func (s *Store) GetReview(ctx context.Context, id int64) (*model.Review, error) {
	return getReview(ctx, s.db, id)
}

func getReview(ctx context.Context, db rowsQueryer, id int64) (*model.Review, error) {
	r, err := scanReviewRow(db.QueryRowContext(ctx, `SELECT `+reviewColumns+` FROM reviews r WHERE r.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("review %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get review %d: %w", id, err)
	}
	if r.Reviewers, err = reviewRoster(ctx, db, r); err != nil {
		return nil, err
	}
	r.Reviewers = nonNil(r.Reviewers)
	if r.Threads, err = reviewThreads(ctx, db, id); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT review_id, round, actor_id, client, base_ref, focused, overall_note, verdict, submitted_at
		   FROM review_rounds WHERE review_id = $1 ORDER BY round`, id)
	if err != nil {
		return nil, fmt.Errorf("load rounds of review %d: %w", id, err)
	}
	r.Rounds, err = collectRows(rows, fmt.Sprintf("load rounds of review %d", id), func(row rowScanner) (model.ReviewRound, error) {
		var rd model.ReviewRound
		err := row.Scan(&rd.Review, &rd.Round, &rd.Actor, &rd.Client, &rd.BaseRef, &rd.Focused,
			&rd.OverallNote, &rd.Verdict, &rd.SubmittedAt)
		return rd, err
	})
	return r, err
}

// reviewThreads loads a review's threads in creation order, each with its
// messages in seq order.
func reviewThreads(ctx context.Context, db rowsQueryer, reviewID int64) ([]model.ReviewThread, error) {
	return threadsWhere(ctx, db, `t.review_id = $1`, reviewID)
}

// ListReviews lists reviews newest-opened last, without threads or rounds.
// Reviewer keeps the reviews whose roster names that actor; Open keeps the
// reviews not yet closed.
func (s *Store) ListReviews(ctx context.Context, p model.ReviewListParams) ([]model.Review, error) {
	var conds []string
	var args []any
	add := func(cond string, arg any) {
		args = append(args, arg)
		conds = append(conds, strings.ReplaceAll(cond, "$?", "$"+strconv.Itoa(len(args))))
	}
	if p.Subject != "" {
		add(`r.subject = $?`, p.Subject)
	}
	if p.State != "" {
		add(`r.state = $?`, p.State)
	}
	if p.Reviewer != "" {
		add(`(EXISTS (SELECT 1 FROM doc_reviewers x WHERE x.doc_id = r.doc_id AND x.actor_id = $?)
		   OR EXISTS (SELECT 1 FROM task_reviewers x WHERE x.task_id = r.task_id AND x.actor_id = $?))`, p.Reviewer)
	}
	if p.Open {
		conds = append(conds, `r.closed_at IS NULL`)
	}
	q := `SELECT ` + reviewColumns + ` FROM reviews r`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, " AND ")
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY r.opened_at, r.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	out, err := collectRows(rows, "list reviews", byValue(scanReviewRow))
	if err != nil {
		return nil, err
	}
	// ponytail: one roster query per review; batch it if review lists grow long.
	for i := range out {
		if out[i].Reviewers, err = reviewRoster(ctx, s.db, &out[i]); err != nil {
			return nil, err
		}
		out[i].Reviewers = nonNil(out[i].Reviewers)
	}
	return out, nil
}

// ReviewEvents reads what review id gained after message seq after: its
// messages with a higher seq and their threads, plus threads resolved at or
// after that message's created_at. No column records when a thread's status
// changed in seq order, so the status arm compares times; a resolve in the
// same second as message after can come back more than once.
func (s *Store) ReviewEvents(ctx context.Context, id, after int64) (*model.ReviewEvents, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM reviews WHERE id = $1)`, id).Scan(&exists); err != nil {
		return nil, fmt.Errorf("read review %d: %w", id, err)
	}
	if !exists {
		return nil, fmt.Errorf("review %d: %w", id, ErrNotFound)
	}
	threads, err := threadsWhere(ctx, s.db, `t.review_id = $1 AND (
		EXISTS (SELECT 1 FROM review_messages m WHERE m.thread_id = t.id AND m.seq > $2)
		OR t.resolved_at >= (SELECT created_at FROM review_messages WHERE review_id = $1 AND seq = $2))`, id, after)
	if err != nil {
		return nil, err
	}
	ev := &model.ReviewEvents{After: after, Threads: nonNil(threads), Messages: []model.ReviewMessage{}}
	for i := range ev.Threads {
		for _, m := range ev.Threads[i].Messages {
			if int64(m.Seq) > after {
				ev.Messages = append(ev.Messages, m)
				ev.After = max(ev.After, int64(m.Seq))
			}
		}
		ev.Threads[i].Messages = nil
	}
	slices.SortFunc(ev.Messages, func(a, b model.ReviewMessage) int { return a.Seq - b.Seq })
	return ev, nil
}

// SetTaskReviewers replaces a task's reviewer roster with reviewers
// (WL-REQ-1322). Every reviewer must be an existing actor and the task must
// not be closed. Reads return the roster in assignment order, so each insert
// is stamped a microsecond after the one before it.
func SetTaskReviewers(tx *sql.Tx, now time.Time, taskID, actorID string, reviewers []string, eventID int64) error {
	var closed bool
	err := tx.QueryRow(`SELECT `+taskClosed("t")+` FROM tasks t WHERE t.id = $1 FOR UPDATE`, taskID).Scan(&closed)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("look up task %s: %w", taskID, err)
	}
	if closed {
		return fmt.Errorf("task %s is closed; its reviewers cannot change: %w", taskID, ErrInvalidInput)
	}
	for _, r := range reviewers {
		if err := checkActorExists(tx, r); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM task_reviewers WHERE task_id = $1`, taskID); err != nil {
		return fmt.Errorf("clear reviewers for task %s: %w", taskID, err)
	}
	for i, r := range reviewers {
		if _, err := tx.Exec(
			`INSERT INTO task_reviewers (task_id, actor_id, assigned_at) VALUES ($1, $2, $3)
			 ON CONFLICT DO NOTHING`,
			taskID, r, now.UTC().Add(time.Duration(i)*time.Microsecond)); err != nil {
			return fmt.Errorf("assign reviewer %s to task %s: %w", r, taskID, err)
		}
	}
	return nil
}

// taskReviewers is a task's reviewer roster in assignment order.
func taskReviewers(ctx context.Context, db rowsQueryer, taskID string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT actor_id FROM task_reviewers WHERE task_id = $1 ORDER BY assigned_at, actor_id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("load reviewers for task %s: %w", taskID, err)
	}
	return collectRows(rows, "load reviewers for task "+taskID, scanString)
}

// TaskReviewers reads a task's reviewer roster.
func (s *Store) TaskReviewers(ctx context.Context, taskID string) ([]string, error) {
	return taskReviewers(ctx, s.db, taskID)
}
