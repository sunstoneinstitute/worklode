package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// PutReviewGuide stores guide as the review's guide for its current round,
// replacing one already there. Only the review's author may propose
// (WL-RULE-1320). from_reading_diff is always false: reading diffs are not
// built.
func PutReviewGuide(tx *sql.Tx, now time.Time, reviewID int64, guide model.ReviewGuide, proposedBy string, refused int) error {
	rev, err := lockReview(tx, reviewID)
	if err != nil {
		return err
	}
	author, err := reviewAuthor(tx, rev)
	if err != nil {
		return err
	}
	if author == "" || proposedBy != author {
		return fmt.Errorf("%s is not the author of review %d: %w", proposedBy, reviewID, ErrForbidden)
	}
	guide.Round, guide.Stale = rev.Round, false
	raw, err := json.Marshal(guide)
	if err != nil {
		return fmt.Errorf("encode guide of review %d: %w", reviewID, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO review_guides (review_id, round, guide, proposed_by, skims_refused, from_reading_diff, created_at)
		 VALUES ($1, $2, $3, $4, $5, false, $6)
		 ON CONFLICT (review_id, round) DO UPDATE
		   SET guide = EXCLUDED.guide, proposed_by = EXCLUDED.proposed_by,
		       skims_refused = EXCLUDED.skims_refused, created_at = EXCLUDED.created_at`,
		reviewID, rev.Round, raw, proposedBy, refused, now.UTC().Truncate(time.Second)); err != nil {
		return fmt.Errorf("store guide of review %d: %w", reviewID, err)
	}
	return nil
}

// GetReviewGuide returns the review's latest guide, Stale when the review has
// moved past the round it was proposed at (WL-REQ-1326). No guide is ErrNotFound.
func (s *Store) GetReviewGuide(ctx context.Context, reviewID int64) (model.ReviewGuide, error) {
	var round int
	if err := s.db.QueryRowContext(ctx, `SELECT round FROM reviews WHERE id = $1`, reviewID).Scan(&round); errors.Is(err, sql.ErrNoRows) {
		return model.ReviewGuide{}, fmt.Errorf("review %d: %w", reviewID, ErrNotFound)
	} else if err != nil {
		return model.ReviewGuide{}, fmt.Errorf("read round of review %d: %w", reviewID, err)
	}
	g, err := latestGuide(ctx, s.db, reviewID, round)
	if err != nil {
		return model.ReviewGuide{}, err
	}
	return *g, nil
}

// latestGuide reads the highest-round guide of a review; reviewRound is the
// review's current round.
func latestGuide(ctx context.Context, db rowsQueryer, reviewID int64, reviewRound int) (*model.ReviewGuide, error) {
	var raw []byte
	var round int
	err := db.QueryRowContext(ctx,
		`SELECT round, guide FROM review_guides WHERE review_id = $1 ORDER BY round DESC LIMIT 1`, reviewID).Scan(&round, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("review %d has no guide: %w", reviewID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read guide of review %d: %w", reviewID, err)
	}
	var g model.ReviewGuide
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("decode guide of review %d: %w", reviewID, err)
	}
	g.Round, g.Stale = round, round < reviewRound
	if g.Files == nil {
		g.Files = []model.GuideFile{}
	}
	return &g, nil
}
