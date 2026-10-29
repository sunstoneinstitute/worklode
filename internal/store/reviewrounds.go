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
// resolves the reviewer's approvals lane at base_ref (WL-REQ-1324).
func SubmitReviewerRound(tx *sql.Tx, now time.Time, reviewID int64, in model.ReviewRoundInput, actorID string, eventID int64) (*model.Review, error) {
	decision, ok := verdictDecisions[in.Verdict]
	if !ok {
		return nil, fmt.Errorf("verdict %q is not approved or changes_requested: %w", in.Verdict, ErrInvalidInput)
	}
	if !roundClients[in.Client] {
		return nil, fmt.Errorf("client %q is not crit, web, cli or agent: %w", in.Client, ErrInvalidInput)
	}
	ctx := context.Background()
	rev, err := lockReview(tx, reviewID)
	if err != nil {
		return nil, err
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
	return r, nil
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
	rev, err := lockReview(tx, reviewID)
	if err != nil {
		return nil, err
	}
	if rev.Doc == 0 {
		return nil, fmt.Errorf("author rounds on a change come with review surface part 2 (WL-PLAN-133): %w", ErrInvalidInput)
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
