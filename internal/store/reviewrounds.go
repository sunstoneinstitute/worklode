package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/review"
)

// roundClients are the review_rounds_client_known CHECK's values.
var roundClients = map[string]bool{"crit": true, "web": true, "cli": true, "agent": true}

// verdictDecisions maps a round verdict (review_rounds_verdict_known) to the
// approvals decision that resolves the reviewer's lane (WL-REQ-1324).
var verdictDecisions = map[string]string{"approved": "approve", "changes_requested": "request_changes"}

// SubmitReviewerRound records one reviewer's round: the threads it opens and
// its verdict at the review's base_ref. An approval is refused while a
// change_request thread is open (WL-REQ-1777). On a document the verdict
// resolves the reviewer's approvals lane at base_ref (WL-REQ-1324), which is
// where the document's self-approval rule and its flow exception live. On a
// change, a verdict from the lease holder or the assignee is refused
// (WL-RULE-1321), and changes_requested moves an in_review task back to
// in_progress, lease untouched (WL-REQ-1324). An approval over an open
// change_request never gets that far, so the verdict alone decides.
func SubmitReviewerRound(tx *sql.Tx, now time.Time, reviewID int64, in model.ReviewRoundInput, actorID string, eventID int64) (*model.Review, error) {
	decision, ok := verdictDecisions[in.Verdict]
	if !ok {
		return nil, fmt.Errorf("verdict %q is not approved or changes_requested: %w", in.Verdict, ErrInvalidInput)
	}
	if !roundClients[in.Client] {
		return nil, fmt.Errorf("client %q is not crit, web, cli or agent: %w", in.Client, ErrInvalidInput)
	}
	ctx := context.Background()
	taskState, err := lockReviewTask(tx, reviewID)
	if err != nil {
		return nil, err
	}
	rev, err := lockReview(tx, reviewID)
	if err != nil {
		return nil, err
	}
	if rev.Task != "" {
		if err := checkNotSelfApproval(tx, rev.Task, actorID); err != nil {
			return nil, err
		}
	}
	roster, err := reviewRoster(ctx, tx, rev)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(roster, actorID) {
		return nil, fmt.Errorf("%s is not a reviewer on review %d: %w", actorID, reviewID, ErrForbidden)
	}
	for _, th := range in.Threads {
		if _, err := OpenThread(tx, now, reviewID, th, actorID, "", "", eventID); err != nil {
			return nil, err
		}
	}
	threads, err := reviewThreads(ctx, tx, reviewID)
	if err != nil {
		return nil, err
	}
	if in.Verdict == "approved" && review.BlocksApproval(threads) {
		var open []string
		for _, t := range threads {
			if t.Status == "open" && t.Intent == "change_request" {
				open = append(open, strconv.FormatInt(t.ID, 10))
			}
		}
		return nil, fmt.Errorf("review %d cannot be approved while change_request threads %s are open: %w",
			reviewID, strings.Join(open, ", "), ErrInvalidInput)
	}

	ts := now.UTC().Truncate(time.Second)
	if _, err := tx.Exec(
		`INSERT INTO review_rounds (review_id, round, actor_id, client, base_ref, focused, overall_note, verdict, submitted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		reviewID, rev.Round+1, actorID, in.Client, rev.BaseRef, in.Focused, in.OverallNote, in.Verdict, ts); err != nil {
		return nil, fmt.Errorf("record round of review %d: %w", reviewID, err)
	}
	if _, err := tx.Exec(`UPDATE reviews SET round = round + 1 WHERE id = $1`, reviewID); err != nil {
		return nil, fmt.Errorf("advance round of review %d: %w", reviewID, err)
	}
	if rev.Doc != 0 {
		if err := decideReviewerLane(tx, ts, rev, actorID, decision); err != nil {
			return nil, err
		}
	}
	r, err := getReview(ctx, tx, reviewID)
	if err != nil {
		return nil, err
	}
	r.State = review.State(r.Reviewers, r.Rounds, r.BaseRef, review.BlocksApproval(r.Threads))
	if _, err := tx.Exec(`UPDATE reviews SET state = $2 WHERE id = $1`, reviewID, r.State); err != nil {
		return nil, fmt.Errorf("set state of review %d: %w", reviewID, err)
	}
	subject := iri.CURIE(iri.Review(reviewID))
	if err := recordReviewEvent(tx, ts, "wl:ReviewRoundSubmitted", subject, actorID, eventID, map[string]any{
		"wl:reviewRole": "reviewer", "wl:client": in.Client, "wl:focused": in.Focused, "wl:threadCount": len(in.Threads),
	}); err != nil {
		return nil, err
	}
	if err := recordReviewEvent(tx, ts, "wl:ReviewVerdictRecorded", subject, actorID, eventID,
		map[string]any{"wl:reviewVerdict": "wlc:" + in.Verdict}); err != nil {
		return nil, err
	}
	if taskState == "in_review" && in.Verdict == "changes_requested" {
		if err := transitionKnown(tx, ts, rev.Task, taskState, "in_review", "in_progress", eventID); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// lockReviewTask locks the task a change review is over and returns its state,
// or "" for a document review. Lock order is task, then review: the close hook
// in transitionKnown holds the task when it reaches the review, so a writer
// that may transition the task takes the task first.
func lockReviewTask(tx *sql.Tx, reviewID int64) (string, error) {
	var state string
	err := tx.QueryRow(
		`SELECT t.state FROM tasks t JOIN reviews r ON r.task_id = t.id WHERE r.id = $1 FOR UPDATE OF t`,
		reviewID).Scan(&state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("lock task of review %d: %w", reviewID, err)
	}
	return state, nil
}

// checkNotSelfApproval refuses a verdict from the task's active lease holder
// or its assignee (WL-RULE-1321). No row records the agent session behind a
// round, so the lease holder stands in for it.
// ponytail: an author whose lease ended and who is not the assignee passes.
func checkNotSelfApproval(tx *sql.Tx, taskID, actorID string) error {
	var self bool
	if err := tx.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM tasks t
		                  LEFT JOIN leases l ON l.task_id = t.id AND l.released_at IS NULL
		                 WHERE t.id = $1 AND $2 IN (t.assignee, l.actor_id))`,
		taskID, actorID).Scan(&self); err != nil {
		return fmt.Errorf("read lease and assignee of task %s: %w", taskID, err)
	}
	if self {
		return fmt.Errorf("self-approval: %s holds the lease or the assignment of task %s: %w", actorID, taskID, ErrForbidden)
	}
	return nil
}

// decideReviewerLane resolves the reviewer's approvals lane at the review's
// base_ref. The lane is created first when the reviewer joined the roster
// after it was requested; a lane already in the verdict's state is left
// alone, so a repeated approval is not an error.
func decideReviewerLane(tx *sql.Tx, ts time.Time, rev *model.Review, actorID, decision string) error {
	entity := DocEntityID(rev.Doc)
	if _, err := InsertAwaitingApproval(tx, ts, "doc", entity, rev.BaseRef, actorID, nil, &actorID, nil); err != nil {
		return err
	}
	lane, err := ApprovalByKey(tx, "doc", entity, rev.BaseRef, actorID)
	if err != nil {
		return err
	}
	if want, _ := DecisionState(decision); lane.State == want {
		return nil
	}
	_, err = DecideApproval(tx, DecideInput{ApprovalID: lane.ID, Decision: decision, ActorID: actorID, Now: ts})
	return err
}

// SubmitAuthorRound records the author's new round on a document review: the
// author read version in.BaseRef, which must be the document's current
// version and differ from the reviewed one. It is refused while a question is
// open (WL-REQ-1774). Every earlier verdict stops counting (WL-REQ-1777),
// and each reviewer gets an awaiting lane at the new version.
func SubmitAuthorRound(tx *sql.Tx, now time.Time, reviewID int64, in model.ReviewRoundInput, actorID string, eventID int64) (*model.Review, error) {
	if in.Verdict != "" {
		return nil, fmt.Errorf("an author round carries no verdict: %w", ErrInvalidInput)
	}
	if !roundClients[in.Client] {
		return nil, fmt.Errorf("client %q is not crit, web, cli or agent: %w", in.Client, ErrInvalidInput)
	}
	ctx := context.Background()
	if _, err := lockReviewTask(tx, reviewID); err != nil {
		return nil, err
	}
	rev, err := lockReview(tx, reviewID)
	if err != nil {
		return nil, err
	}
	author, err := reviewAuthor(tx, rev)
	if err != nil {
		return nil, err
	}
	if author == "" || actorID != author {
		return nil, fmt.Errorf("%s is not the author of review %d: %w", actorID, reviewID, ErrForbidden)
	}
	threads, err := reviewThreads(ctx, tx, reviewID)
	if err != nil {
		return nil, err
	}
	if review.OpenQuestion(threads) {
		return nil, fmt.Errorf("review %d has an open question; answer it in its thread first: %w", reviewID, ErrInvalidInput)
	}
	if rev.Doc == 0 {
		return submitChangeAuthorRound(tx, now, rev, threads, in, actorID, eventID)
	}
	var version int
	if err := tx.QueryRow(`SELECT version FROM docs WHERE id = $1`, rev.Doc).Scan(&version); err != nil {
		return nil, fmt.Errorf("read version of doc %d: %w", rev.Doc, err)
	}
	current := strconv.Itoa(version)
	if in.BaseRef != current {
		return nil, fmt.Errorf("doc %d is at version %s, not %q; read it again: %w", rev.Doc, current, in.BaseRef, ErrInvalidInput)
	}
	if in.BaseRef == rev.BaseRef {
		return nil, fmt.Errorf("nothing new to review: doc %d is still at version %s; a `lode rule edit` moves no version, so write the spec through `lode doc edit`: %w",
			rev.Doc, current, ErrInvalidInput)
	}
	ts := now.UTC().Truncate(time.Second)
	if _, err := tx.Exec(`UPDATE reviews SET base_ref = $2, round = round + 1, state = 'open' WHERE id = $1`,
		reviewID, current); err != nil {
		return nil, fmt.Errorf("advance review %d: %w", reviewID, err)
	}
	if err := RequestDocApproval(tx, ts, rev.Doc, version); err != nil {
		return nil, err
	}
	if err := recordReviewEvent(tx, ts, "wl:ReviewRoundSubmitted", iri.CURIE(iri.Review(reviewID)), actorID, eventID, map[string]any{
		"wl:reviewRole": "author", "wl:client": in.Client, "wl:focused": in.Focused, "wl:threadCount": 0,
	}); err != nil {
		return nil, err
	}
	return getReview(ctx, tx, reviewID)
}

// submitChangeAuthorRound is SubmitAuthorRound for a change review: in.BaseRef
// is the new head sha, in.Reanchors say where each open thread's line went
// (WL-REQ-1317). The server never sees the repository, so the client computes
// them. A thread left unnamed stays as it was. The task moves back to
// in_review when it was in_progress.
func submitChangeAuthorRound(tx *sql.Tx, now time.Time, rev *model.Review, threads []model.ReviewThread,
	in model.ReviewRoundInput, actorID string, eventID int64) (*model.Review, error) {
	if !gitSHA.MatchString(in.BaseRef) {
		return nil, fmt.Errorf("base_ref must be a 40-hex git sha, got %q: %w", in.BaseRef, ErrInvalidInput)
	}
	if in.BaseRef == rev.BaseRef {
		return nil, fmt.Errorf("nothing new to review: review %d is already at %s: %w", rev.ID, in.BaseRef, ErrInvalidInput)
	}
	open := map[int64]bool{}
	for _, t := range threads {
		open[t.ID] = t.Status == "open"
	}
	ts := now.UTC().Truncate(time.Second)
	for _, ra := range in.Reanchors {
		if !open[ra.Thread] {
			return nil, fmt.Errorf("thread %d is not an open thread of review %d: %w", ra.Thread, rev.ID, ErrInvalidInput)
		}
		switch {
		case ra.Status == "obsolete":
			if _, err := ResolveThread(tx, ts, ra.Thread, actorID, "obsolete", eventID); err != nil {
				return nil, err
			}
		case ra.Status == "" && ra.Anchor.Line > 0 && ra.Anchor.BlobSHA != "":
			if _, err := tx.Exec(`UPDATE review_threads SET line_number = $2, blob_sha = $3 WHERE id = $1`,
				ra.Thread, ra.Anchor.Line, ra.Anchor.BlobSHA); err != nil {
				return nil, fmt.Errorf("re-anchor thread %d: %w", ra.Thread, err)
			}
		default:
			return nil, fmt.Errorf("re-anchor of thread %d needs a line and blob_sha, or status obsolete: %w", ra.Thread, ErrInvalidInput)
		}
		open[ra.Thread] = false
	}
	if _, err := tx.Exec(`UPDATE reviews SET base_ref = $2, round = round + 1, state = 'open' WHERE id = $1`,
		rev.ID, in.BaseRef); err != nil {
		return nil, fmt.Errorf("advance review %d: %w", rev.ID, err)
	}
	if err := recordReviewEvent(tx, ts, "wl:ReviewRoundSubmitted", iri.CURIE(iri.Review(rev.ID)), actorID, eventID, map[string]any{
		"wl:reviewRole": "author", "wl:client": in.Client, "wl:focused": in.Focused, "wl:threadCount": 0,
	}); err != nil {
		return nil, err
	}
	var state string
	if err := tx.QueryRow(`SELECT state FROM tasks WHERE id = $1`, rev.Task).Scan(&state); err != nil {
		return nil, fmt.Errorf("read state of task %s: %w", rev.Task, err)
	}
	if state == "in_progress" {
		if err := Transition(tx, ts, rev.Task, "in_progress", "in_review", eventID); err != nil {
			return nil, err
		}
	}
	return getReview(context.Background(), tx, rev.ID)
}

// CloseReview closes an open review with outcome and emits wl:ReviewClosed.
func CloseReview(tx *sql.Tx, now time.Time, reviewID int64, outcome string, eventID int64) error {
	if _, err := lockReview(tx, reviewID); err != nil {
		return err
	}
	ts := now.UTC().Truncate(time.Second)
	if _, err := tx.Exec(`UPDATE reviews SET state = 'closed', closed_at = $2 WHERE id = $1`, reviewID, ts); err != nil {
		return fmt.Errorf("close review %d: %w", reviewID, err)
	}
	return recordReviewEvent(tx, ts, "wl:ReviewClosed", iri.CURIE(iri.Review(reviewID)), "", eventID,
		map[string]any{"wl:outcome": outcome})
}

// closeTaskReview closes the task's open review, if any, with outcome. The
// caller holds the task row, and CloseReview moves no task, so a close cannot
// re-enter transitionKnown.
func closeTaskReview(tx *sql.Tx, now time.Time, taskID, outcome string, eventID int64) error {
	var id int64
	err := tx.QueryRow(`SELECT id FROM reviews WHERE task_id = $1 AND closed_at IS NULL`, taskID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read open review of task %s: %w", taskID, err)
	}
	return CloseReview(tx, now, id, outcome, eventID)
}

// escalateOnQuestions replies on each open question of the task's open review,
// so the reviewer sees why the answer is late (WL-REQ-1774). The reply's
// author role comes from the lease, so this runs before the lease is released.
func escalateOnQuestions(tx *sql.Tx, now time.Time, taskID, escalation, actorID string, eventID int64) error {
	rows, err := tx.Query(
		`SELECT th.id FROM review_threads th JOIN reviews r ON r.id = th.review_id
		  WHERE r.task_id = $1 AND r.closed_at IS NULL AND th.intent = 'question' AND th.status = 'open'
		  ORDER BY th.id`, taskID)
	if err != nil {
		return fmt.Errorf("open questions of task %s: %w", taskID, err)
	}
	threads, err := collectRows(rows, "open questions of task "+taskID, func(row rowScanner) (int64, error) {
		var id int64
		err := row.Scan(&id)
		return id, err
	})
	if err != nil || len(threads) == 0 {
		return err
	}
	// Task before review, the order lockReviewTask states.
	if _, err := tx.Exec(`SELECT 1 FROM tasks WHERE id = $1 FOR UPDATE`, taskID); err != nil {
		return fmt.Errorf("lock task %s: %w", taskID, err)
	}
	body := fmt.Sprintf("Escalated to %s: the plan or spec does not cover this, so the answer waits on it.", escalation)
	for _, th := range threads {
		if _, _, _, err := addReply(tx, now, th, body, actorID, eventID, true); err != nil {
			return err
		}
	}
	return nil
}

// closeDocReview closes the document's open review, if any, with outcome.
func closeDocReview(tx *sql.Tx, now time.Time, docID int64, outcome string, eventID int64) error {
	var id int64
	err := tx.QueryRow(`SELECT id FROM reviews WHERE doc_id = $1 AND closed_at IS NULL`, docID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read open review of doc %d: %w", docID, err)
	}
	return CloseReview(tx, now, id, outcome, eventID)
}
