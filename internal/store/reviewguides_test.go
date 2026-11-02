package store

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func putGuide(t *testing.T, s *Store, reviewID int64, g model.ReviewGuide, actor string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "review.guide", nil,
		func(tx *sql.Tx, _ int64) error { return PutReviewGuide(tx, taskTestNow, reviewID, g, actor, 0) })
	return err
}

func TestReviewGuide(t *testing.T) {
	s, _, r, _, _, _ := gateFixture(t)
	if _, err := s.GetReviewGuide(t.Context(), r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get with no guide: %v, want ErrNotFound", err)
	}
	g := model.ReviewGuide{Overview: "o", Files: []model.GuideFile{{Path: "sec-1", Orientation: "start here"}}, DiffBlob: "abc"}
	if err := putGuide(t, s, r.ID, g, "a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("put by a non-author: %v, want ErrForbidden", err)
	}
	if err := putGuide(t, s, r.ID, g, "stig"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetReviewGuide(t.Context(), r.ID)
	if err != nil || got.Stale || got.Round != r.Round || got.Overview != "o" || got.DiffBlob != "abc" {
		t.Fatalf("get = %+v, %v; want fresh guide at round %d", got, err, r.Round)
	}
	if rv, err := s.GetReview(t.Context(), r.ID); err != nil || rv.Guide == nil || rv.Guide.Overview != "o" {
		t.Fatalf("GetReview guide = %+v, %v", rv.Guide, err)
	}
	if _, err := updateDocBody(t, s, r.Doc, gateSpecBody+"\nmore.\n"); err != nil {
		t.Fatal(err)
	}
	rv, err := authorRound(t, s, r.ID, "2", "stig")
	if err != nil {
		t.Fatal(err)
	}
	if got, err = s.GetReviewGuide(t.Context(), r.ID); err != nil || !got.Stale || got.Round != r.Round {
		t.Fatalf("after author round: %+v, %v; want stale at round %d", got, err, r.Round)
	}
	g.Overview = "o2"
	if err := putGuide(t, s, r.ID, g, "stig"); err != nil {
		t.Fatal(err)
	}
	if got, err = s.GetReviewGuide(t.Context(), r.ID); err != nil || got.Stale || got.Round != rv.Round || got.Overview != "o2" {
		t.Fatalf("after re-put: %+v, %v; want fresh at round %d", got, err, rv.Round)
	}
}
