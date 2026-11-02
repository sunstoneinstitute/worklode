package api

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/review"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// putReviewGuide handles PUT /api/v1/reviews/{id}/guide (WL-REQ-1323). The
// store refuses anyone but the review's author with ErrForbidden. Skims stay
// in storage and are stripped at read, so one row serves both reads.
func (s *server) putReviewGuide(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok || !s.allowReview(w, r, id, permTaskWrite) {
		return
	}
	var req model.GuideProposal
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	rev, err := s.st.GetReview(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	actorID, now := actorIDFrom(r), s.st.Now()
	guide, refusals := review.Compose(req, true)
	if rev.Task != "" && req.Diff != "" {
		guide.DiffBlob = s.storeReviewDiff(r, rev, req.Diff, actorID)
	}
	logged := req
	logged.Diff = "" // the diff lives in the blob; keep it out of the event log
	if err := s.recordReviewEvent(w, r, "review.guide_proposed", map[string]any{"review": id}, logged,
		func(tx *sql.Tx, _ int64) error {
			return store.PutReviewGuide(tx, now, id, guide, actorID, len(refusals))
		}); err != nil {
		return
	}
	out := model.ReviewGuideResult{Guide: guide, Refused: []model.GuideRefusal{}}
	out.Guide.Round = rev.Round
	for _, f := range guide.Files {
		for _, sp := range f.Skims {
			s.reviewMetrics.Skimmed(sp.DecidedBy)
		}
	}
	for _, rf := range refusals {
		s.reviewMetrics.SkimRefused(rf.Rule)
		out.Refused = append(out.Refused, model.GuideRefusal{Path: rf.Path, Rule: rf.Rule})
	}
	writeJSON(w, http.StatusOK, out)
}

// storeReviewDiff stores the round's diff as a task blob so a reader outside
// a worktree can print it, and returns its hash, or "" when there is no blob
// storage or the store fails: a guide without its diff is still a guide.
func (s *server) storeReviewDiff(r *http.Request, rev *model.Review, diff, actorID string) string {
	if s.blobs == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(diff))
	hash := hex.EncodeToString(sum[:])
	ctx := r.Context()
	if _, err := s.st.GetBlob(ctx, hash); err != nil {
		const mediaType = "text/x-diff"
		if err := s.blobs.Put(ctx, blobstore.Key(hash), bytes.NewReader([]byte(diff)), int64(len(diff)), mediaType); err != nil {
			s.log.Warn("store review diff", "review", rev.ID, "err", err)
			return ""
		}
		if _, err := s.st.InsertBlob(ctx, hash, mediaType, int64(len(diff))); err != nil {
			s.log.Warn("store review diff", "review", rev.ID, "err", err)
			return ""
		}
	}
	name := fmt.Sprintf("review-%d-round-%d.diff", rev.ID, rev.Round)
	if err := s.st.AttachBlob(ctx, rev.Task, hash, name, actorID); err != nil {
		s.log.Warn("attach review diff", "review", rev.ID, "err", err)
		return ""
	}
	return hash
}

// getReviewGuide handles GET /api/v1/reviews/{id}/guide?focused=. It is the
// only source of a guide for a reviewer (WL-RULE-1321).
func (s *server) getReviewGuide(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok || !s.allowReview(w, r, id, permTaskRead) {
		return
	}
	var p model.ReviewGuideParams
	if err := readQuery(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	g, err := s.st.GetReviewGuide(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if !p.Focused {
		for i := range g.Files {
			g.Files[i].Skims = nil
		}
	}
	writeJSON(w, http.StatusOK, g)
}
