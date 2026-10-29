package api

import (
	"database/sql"
	"maps"
	"net/http"
	"strconv"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// The review routes of WL-REQ-1323 for document and rule subjects: open and
// read a review, open, reply to and resolve threads, bare notes, and the two
// thread lists. Each write records one request event (review.*) as the parent
// of the WL-REQ-1780 events the store records as children in the same
// transaction, so no domain event is emitted twice.

// pathInt64 reads a positive integer path value, writing 400 when it is not.
func pathInt64(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, name+" must be a positive integer")
		return 0, false
	}
	return id, true
}

// recordReviewEvent is recordDocEvent for a review write: about names the
// review, thread or document, merged with the actor and the request into the
// payload. It writes the error response itself.
func (s *server) recordReviewEvent(w http.ResponseWriter, r *http.Request, eventType string,
	about map[string]any, req any, apply func(tx *sql.Tx, eventID int64) error) error {
	payload := map[string]any{"actor": actorIDFrom(r), "request": req}
	maps.Copy(payload, about)
	if err := s.recordEvent(r.Context(), docSource, eventType, payload, apply); err != nil {
		s.mapStoreErr(w, err)
		return err
	}
	return nil
}

// refreshOpenThreads sets worklode_review_open_threads from a count after a
// thread write. A failed count leaves the gauge as it was.
func (s *server) refreshOpenThreads(r *http.Request) {
	counts, err := s.st.OpenThreadCounts(r.Context())
	if err != nil {
		s.log.Warn("count open review threads", "err", err)
		return
	}
	s.reviewMetrics.SetOpenThreads(counts)
}

// openReview handles POST /api/v1/reviews.
func (s *server) openReview(w http.ResponseWriter, r *http.Request) {
	var req model.OpenReviewInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actorID, now := actorIDFrom(r), s.st.Now()
	var rev *model.Review
	if err := s.recordReviewEvent(w, r, "review.opened", map[string]any{"doc": req.Doc}, req,
		func(tx *sql.Tx, eventID int64) (err error) {
			rev, err = store.OpenReview(tx, now, req, actorID, eventID)
			return err
		}); err != nil {
		return
	}
	writeJSON(w, http.StatusCreated, rev)
}

// listReviews handles GET /api/v1/reviews.
func (s *server) listReviews(w http.ResponseWriter, r *http.Request) {
	var p model.ReviewListParams
	if err := readQuery(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := s.st.ListReviews(r.Context(), p)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNilSlice(list))
}

// getReview handles GET /api/v1/reviews/{id}: the review with its threads,
// their messages, and its rounds. `lode review show --json` prints it.
func (s *server) getReview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	rev, err := s.st.GetReview(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

// openReviewThread handles POST /api/v1/reviews/{id}/threads.
func (s *server) openReviewThread(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	s.openThread(w, r, map[string]any{"review": id}, func(tx *sql.Tx, now time.Time, in model.OpenThreadInput, actorID string, eventID int64) (*model.ReviewThread, error) {
		return store.OpenThread(tx, now, id, in, actorID, "", "", eventID)
	})
}

// openDocThread handles POST /api/v1/docs/{id}/threads: a bare note on the
// document or on a rule it arranges, with a section anchor or a range
// (WL-REQ-1319, WL-REQ-1323). A range on a spec is of its rendered body.
func (s *server) openDocThread(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	s.openThread(w, r, map[string]any{"doc": id}, func(tx *sql.Tx, now time.Time, in model.OpenThreadInput, actorID string, eventID int64) (*model.ReviewThread, error) {
		return store.OpenDocThread(tx, now, id, in, actorID, eventID)
	})
}

// openThread is the shared body of the two thread-opening routes.
func (s *server) openThread(w http.ResponseWriter, r *http.Request, about map[string]any,
	open func(tx *sql.Tx, now time.Time, in model.OpenThreadInput, actorID string, eventID int64) (*model.ReviewThread, error)) {
	var req model.OpenThreadInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actorID, now := actorIDFrom(r), s.st.Now()
	var th *model.ReviewThread
	if err := s.recordReviewEvent(w, r, "review.thread_opened", about, req,
		func(tx *sql.Tx, eventID int64) (err error) {
			th, err = open(tx, now, req, actorID, eventID)
			return err
		}); err != nil {
		return
	}
	s.reviewMetrics.Thread(th.Intent)
	s.refreshOpenThreads(r)
	writeJSON(w, http.StatusCreated, th)
}

// threadPath reads {tid}, and on the review-scoped spelling checks that the
// thread belongs to review {id}: a thread of another review, or a bare note,
// is 404 there.
func (s *server) threadPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	tid, ok := pathInt64(w, r, "tid")
	if !ok {
		return 0, false
	}
	if r.PathValue("id") == "" {
		return tid, true
	}
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return 0, false
	}
	th, err := s.st.GetThread(r.Context(), tid)
	if err != nil {
		s.mapStoreErr(w, err)
		return 0, false
	}
	if th.Review != id {
		writeErr(w, http.StatusNotFound, "not found")
		return 0, false
	}
	return tid, true
}

// replyThread handles POST /api/v1/reviews/{id}/threads/{tid}/messages and
// POST /api/v1/threads/{tid}/messages.
func (s *server) replyThread(w http.ResponseWriter, r *http.Request) {
	tid, ok := s.threadPath(w, r)
	if !ok {
		return
	}
	var req model.ThreadReplyInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actorID, now := actorIDFrom(r), s.st.Now()
	var (
		m        model.ReviewMessage
		answered bool
		askedAt  time.Time
	)
	if err := s.recordReviewEvent(w, r, "review.message_added", map[string]any{"thread": tid}, req,
		func(tx *sql.Tx, eventID int64) (err error) {
			m, answered, askedAt, err = store.ReplyThread(tx, now, tid, req.Body, actorID, eventID)
			return err
		}); err != nil {
		return
	}
	if answered {
		s.reviewMetrics.QuestionAnswered(m.CreatedAt.Sub(askedAt))
	}
	writeJSON(w, http.StatusCreated, m)
}

// resolveThread handles POST /api/v1/reviews/{id}/threads/{tid}/resolve and
// POST /api/v1/threads/{tid}/resolve. The status defaults to resolved.
func (s *server) resolveThread(w http.ResponseWriter, r *http.Request) {
	tid, ok := s.threadPath(w, r)
	if !ok {
		return
	}
	var req model.ThreadResolveInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Status == "" {
		req.Status = "resolved"
	}
	actorID, now := actorIDFrom(r), s.st.Now()
	var th *model.ReviewThread
	if err := s.recordReviewEvent(w, r, "review.thread_resolved", map[string]any{"thread": tid}, req,
		func(tx *sql.Tx, eventID int64) (err error) {
			th, err = store.ResolveThread(tx, now, tid, actorID, req.Status, eventID)
			return err
		}); err != nil {
		return
	}
	s.refreshOpenThreads(r)
	writeJSON(w, http.StatusOK, th)
}

// docThreads handles GET /api/v1/docs/{id}/threads: bare notes and review
// threads on the document and the rules it arranges, obsolete included.
func (s *server) docThreads(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var p model.DocThreadsParams
	if err := readQuery(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	threads, err := s.st.ThreadsForSubject(r.Context(), id, 0, p.Version, p.Anchor, p.Status)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNilSlice(threads))
}

// ruleThreads handles GET /api/v1/rules/{id}/threads, {id} a rule ref.
func (s *server) ruleThreads(w http.ResponseWriter, r *http.Request) {
	ref, ok := ruleRef(w, r)
	if !ok {
		return
	}
	var p model.RuleThreadsParams
	if err := readQuery(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := s.st.GetRule(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	threads, err := s.st.ThreadsForSubject(r.Context(), 0, rule.ID, p.Version, "", p.Status)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNilSlice(threads))
}

// nonNilSlice answers an empty list as [] rather than null.
func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// reviewRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var reviewRouteDocs = map[string]routeDoc{
	"POST /api/v1/reviews": {
		summary: "Open a review of a document",
		request: model.OpenReviewInput{},
		responses: map[int]any{http.StatusCreated: model.Review{},
			http.StatusConflict: model.ErrorResponse{}, http.StatusUnprocessableEntity: model.ErrorResponse{}},
	},
	"GET /api/v1/reviews": {
		summary:   "List reviews",
		responses: map[int]any{http.StatusOK: []model.Review{}},
		params:    model.ReviewListParams{},
	},
	"GET /api/v1/reviews/{id}": {
		summary:   "Get a review with its threads, messages and rounds",
		responses: map[int]any{http.StatusOK: model.Review{}},
	},
	"POST /api/v1/reviews/{id}/threads": {
		summary:   "Open a thread on a review",
		request:   model.OpenThreadInput{},
		responses: map[int]any{http.StatusCreated: model.ReviewThread{}, http.StatusUnprocessableEntity: model.ErrorResponse{}},
	},
	"POST /api/v1/reviews/{id}/threads/{tid}/messages": {
		summary:   "Reply to a review thread",
		request:   model.ThreadReplyInput{},
		responses: map[int]any{http.StatusCreated: model.ReviewMessage{}, http.StatusUnprocessableEntity: model.ErrorResponse{}},
	},
	"POST /api/v1/reviews/{id}/threads/{tid}/resolve": {
		summary:   "Resolve a review thread",
		request:   model.ThreadResolveInput{},
		responses: map[int]any{http.StatusOK: model.ReviewThread{}, http.StatusUnprocessableEntity: model.ErrorResponse{}},
	},
	"GET /api/v1/docs/{id}/threads": {
		summary:   "List a document's threads, bare notes and review threads together",
		responses: map[int]any{http.StatusOK: []model.ReviewThread{}},
		params:    model.DocThreadsParams{},
	},
	"GET /api/v1/rules/{id}/threads": {
		summary:   "List a rule's threads, bare notes and review threads together",
		responses: map[int]any{http.StatusOK: []model.ReviewThread{}},
		params:    model.RuleThreadsParams{},
	},
	"POST /api/v1/docs/{id}/threads": {
		summary:   "Open a bare note on a document or one of its arranged rules",
		request:   model.OpenThreadInput{},
		responses: map[int]any{http.StatusCreated: model.ReviewThread{}, http.StatusUnprocessableEntity: model.ErrorResponse{}},
	},
	"POST /api/v1/threads/{tid}/messages": {
		summary:   "Reply to any thread, a bare note included",
		request:   model.ThreadReplyInput{},
		responses: map[int]any{http.StatusCreated: model.ReviewMessage{}, http.StatusUnprocessableEntity: model.ErrorResponse{}},
	},
	"POST /api/v1/threads/{tid}/resolve": {
		summary:   "Resolve any thread, a bare note included",
		request:   model.ThreadResolveInput{},
		responses: map[int]any{http.StatusOK: model.ReviewThread{}, http.StatusUnprocessableEntity: model.ErrorResponse{}},
	},
}
