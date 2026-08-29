// approvals.go serves the two approval surfaces a CLI token may reach:
// requesting review on a document, and reading the awaiting queue.
//
// Deciding is deliberately absent. 029 §7.3 makes approving a web UI act —
// the OIDC session's group claims are fresh, a 30-day CLI token's are not —
// so the only decision route is POST /approvals/{id}/decide, gated by
// requireSession in webform.go. Adding a /api/v1 decide route here would
// defeat that; it is not an oversight.
package api

import (
	"database/sql"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// requestDocApproval handles POST /api/v1/docs/{id}/request-approval: opens
// one awaiting lane per reviewer in the document's durable reviewer set
// (025 §7.3, assigned separately via POST /api/v1/docs/{id}/reviewers —
// WL-359) on its current version. Re-requesting at the same version adds
// only the lanes that are missing, so a caller who has just added a
// reviewer can simply run this again.
//
// The response is the document, as submitDoc's is: what the caller cannot
// derive locally is the version the lanes were opened against, and the
// document carries it. Takes no body: there is nothing left for a caller to
// name once the reviewer set lives in storage.
//
// No dedicated metric. Like every other document verb this goes through
// RecordDocEvent, so its outcomes land on
// worklode_doc_operations_total{op="request_approval"}, and the
// no-reviewers-assigned refusal is a 422 on http_requests_total's
// {route, code}.
func (s *server) requestDocApproval(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	d, err := s.st.GetDoc(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	now := s.st.Now()
	// The version is read before the transaction and written into it. A body
	// edited in between bumps the version, so the lanes would name the older
	// one — harmless: the reviewer set is re-requested per version anyway,
	// and no gate keys off it yet.
	version := d.Version
	if err := s.recordDocEvent(w, r, "request_approval", "doc.approval_requested", id, nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.RequestDocApproval(tx, now, id, version)
		}); err != nil {
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// setDocReviewers handles POST /api/v1/docs/{id}/reviewers: replaces the
// document's durable reviewer set wholesale (025 §7.3, WL-359) — the owner
// or an admin's call, same authority as transferDocOwner checks. Unlike
// request-approval this opens no approval lanes itself; it only changes what
// the next request-approval call reads.
func (s *server) setDocReviewers(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var req model.SetDocReviewersInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actorID := actorIDFrom(r)
	now := s.st.Now()
	if err := s.recordDocEvent(w, r, "set_reviewers", "doc.reviewers_changed", id, req,
		func(tx *sql.Tx, eventID int64) error {
			return store.SetDocReviewers(tx, now, id, actorID, req.Reviewers, eventID)
		}); err != nil {
		return
	}
	s.writeDoc(w, r, id)
}

// listApprovals handles GET /api/v1/approvals: the awaiting queue (029 §7.1)
// as JSON, the same rows the cockpit's /reviews page renders. Read-only, and
// unfiltered — the queue is what is outstanding org-wide, and a queue you
// have to filter to see all of is not a queue.
//
// No dedicated metric, on resolveDocRef's reasoning: every outcome this route
// has is already a status code on http_requests_total's {route, code}.
func (s *server) listApprovals(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.ListAwaitingApprovals(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if rows == nil {
		rows = []model.AwaitingApproval{}
	}
	writeJSON(w, http.StatusOK, model.ApprovalListResponse{Approvals: rows})
}
