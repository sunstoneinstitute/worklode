// approvals.go serves the JSON approval surfaces: requesting review on a
// document, filing an ad-hoc requirement on any governed target (WL-REQ-124),
// reading the awaiting queue, and deciding a row (WL-REQ-1608).
package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// requestDocApproval handles POST /api/v1/docs/{id}/request-approval: opens
// one awaiting lane per reviewer in the document's durable reviewer set
// (WL-REQ-170, assigned separately via POST /api/v1/docs/{id}/reviewers —
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
// document's durable reviewer set wholesale (WL-REQ-170, WL-359) — the owner
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

// requireApproval handles POST /api/v1/approvals: WL-REQ-124's "ad-hoc
// requirements can be added to any governed target". A flow mints rows from
// project policy; this files one by hand, on the same table, so the queue
// reads both without knowing which is which.
//
// Filing is not deciding: an agent or a CI job may say "this needs review",
// and deciding is decideApprovalAPI's separate permission. created_by is the requesting actor, not
// the 'worklode' system actor: a human filed this policy, and only
// rule-created rows belong to worklode (store.FlowActorID).
//
// Re-filing is safe: migration 0064's key is (kind, id, revision, lane), so a
// second call on the same key returns the existing row with 200 rather than a
// conflict, and only an actual insert counts on
// worklode_approval_requirements_total{origin="adhoc"}.
func (s *server) requireApproval(w http.ResponseWriter, r *http.Request) {
	var req model.RequireApprovalInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	req.EntityKind = strings.TrimSpace(req.EntityKind)
	req.EntityID = strings.TrimSpace(req.EntityID)
	if req.EntityID == "" {
		writeErr(w, http.StatusUnprocessableEntity, "entity_id is required")
		return
	}
	if req.Role != "" && req.Actor != "" {
		writeErr(w, http.StatusUnprocessableEntity,
			"role and actor are mutually exclusive: a lane demands a group or a person, not both")
		return
	}

	actor := actorIDFrom(r)
	now := s.st.Now()
	var created *model.Approval
	inserted := false
	if err := s.recordEvent(r.Context(), "cli", "approval.required", map[string]any{
		"entity_kind": req.EntityKind,
		"entity_id":   req.EntityID,
		"revision":    req.Revision,
		"lane":        req.Lane,
		"role":        req.Role,
		"required":    req.Actor,
		"actor":       actor,
	}, func(tx *sql.Tx, _ int64) error {
		// The same call answers "does it exist" (ErrNotFound -> 404) and
		// "what revision does an unqualified requirement bind to".
		revision, err := store.DefaultSubjectRevision(tx, req.EntityKind, req.EntityID)
		if err != nil {
			return err
		}
		if req.Revision != "" {
			revision = req.Revision
		}
		inserted, err = store.InsertAwaitingApproval(tx, now, req.EntityKind,
			req.EntityID, revision, req.Lane,
			textPtr(req.Role), textPtr(req.Actor), textPtr(actor))
		if err != nil {
			return err
		}
		created, err = store.ApprovalByKey(tx, req.EntityKind, req.EntityID, revision, req.Lane)
		return err
	}); err != nil {
		s.mapStoreErr(w, err)
		return
	}

	status := http.StatusOK
	if inserted {
		s.observeApprovalRequirements(originAdhoc, 1)
		status = http.StatusCreated
	}
	writeJSON(w, status, created)
}

// textPtr maps "" to NULL, for the three approvals columns where absent and
// empty are the same thing.
func textPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// listApprovals handles GET /api/v1/approvals: the awaiting queue (WL-REQ-124)
// as JSON, the same rows the cockpit's /reviews page renders. Read-only.
// Excludes a pr-kind row whose PR has since closed (WL-663): tasks carry no
// review requirement by default (WL-REQ-124), so a closed, unreviewed PR is
// not an outstanding requirement, just an ingest artifact nobody can act on
// any more.
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

// groupsFreshFor is how old a token subject's group sync may be before it can
// decide a role-gated approval (WL-REQ-1608).
const groupsFreshFor = 24 * time.Hour

// errGroupsStale is the WL-REQ-1608 refusal: the answer says to log in again.
const errGroupsStale = "your group membership was last synced more than 24 hours ago: " +
	"log in again (lode login, or reconnect the MCP connector) to decide this approval"

// decideApprovalAPI handles POST /api/v1/approvals/{id}/decide (WL-REQ-1608):
// the same store.DecideApproval the cockpit form calls, with the subject's
// actor as the decider. A task-scoped token never reaches it (routeGuards).
// A token subject deciding a role-gated row must have had its groups synced
// within groupsFreshFor; a session subject is fresh by construction.
func (s *server) decideApprovalAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req model.DecideApprovalInput
	if err := readJSON(w, r, &req); err != nil {
		s.observeApprovalDecision(decisionInvalid, decisionInvalid)
		writeBodyErr(w, err)
		return
	}
	verdict := strings.ReplaceAll(strings.TrimSpace(req.Verdict), " ", "_")
	if _, ok := store.DecisionState(verdict); !ok {
		s.observeApprovalDecision(decisionInvalid, decisionInvalid)
		writeErr(w, http.StatusUnprocessableEntity,
			"verdict must be approve, request_changes or reject")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.observeApprovalDecision(verdict, "not_found")
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	sub := subjectFrom(r)
	if sub.Via == authToken {
		stale, err := s.groupsStaleFor(ctx, id, sub.ActorID)
		if err != nil {
			s.observeApprovalDecision(verdict, approvalDecisionOutcome(err))
			s.mapStoreErr(w, err)
			return
		}
		if stale {
			s.observeApprovalDecision(verdict, "refused_stale")
			writeErr(w, http.StatusForbidden, errGroupsStale)
			return
		}
	}

	var decided *model.Approval
	err = s.recordEvent(ctx, "cli", "approval.decided", map[string]any{
		"approval_id": id, "decision": verdict, "actor": sub.ActorID, "note": req.Note,
	}, func(tx *sql.Tx, _ int64) error {
		var err error
		decided, err = store.DecideApproval(tx, store.DecideInput{
			ApprovalID: id,
			Decision:   verdict,
			ActorID:    sub.ActorID,
			Groups:     sub.Groups,
			Now:        s.st.Now(),
		})
		return err
	})
	s.observeApprovalDecision(verdict, approvalDecisionOutcome(err))
	if err != nil {
		s.approvalDecideAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decided)
}

// groupsStaleFor reports whether approval id is role-gated and actorID's
// groups were synced longer than groupsFreshFor ago, or never. A row that
// names an actor or no role needs no group check (WL-REQ-1608).
func (s *server) groupsStaleFor(ctx context.Context, id int64, actorID string) (bool, error) {
	a, err := s.st.GetApproval(ctx, id)
	if err != nil {
		return false, err
	}
	if a.RequiredRole == nil || *a.RequiredRole == "" {
		return false, nil
	}
	actor, err := s.st.GetActor(ctx, actorID)
	if err != nil {
		return false, err
	}
	return actor.GroupsSyncedAt == nil ||
		s.st.Now().Sub(*actor.GroupsSyncedAt) > groupsFreshFor, nil
}

// approvalDecideAPIErr is approvalActErr for the JSON API: the same refusals,
// as JSON errors.
func (s *server) approvalDecideAPIErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrApprovalResolved):
		writeErr(w, http.StatusConflict, "this approval has already been decided")
	case errors.Is(err, store.ErrNoRevision):
		writeErr(w, http.StatusUnprocessableEntity, "nothing has been designated for review yet")
	case errors.Is(err, store.ErrSelfApproval):
		writeErr(w, http.StatusForbidden, "you authored this change, so you cannot decide it")
	case errors.Is(err, store.ErrNotQualified):
		writeErr(w, http.StatusForbidden, "deciding this approval needs a role you do not hold")
	case errors.Is(err, store.ErrNotPriorApprover):
		writeErr(w, http.StatusForbidden,
			"only someone who approved this entity can say whether that decision still holds")
	default:
		s.mapStoreErr(w, err)
	}
}

// approvalRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var approvalRouteDocs = map[string]routeDoc{
	"GET /api/v1/approvals": {
		summary:   "List the awaiting approval queue",
		responses: map[int]any{http.StatusOK: model.ApprovalListResponse{}},
	},
	"POST /api/v1/approvals": {
		summary: "File an ad-hoc approval requirement on a governed target",
		request: model.RequireApprovalInput{},
		// 200 when the requirement already existed, 201 on a fresh insert.
		responses: map[int]any{
			http.StatusOK:      model.Approval{},
			http.StatusCreated: model.Approval{},
		},
	},
	"POST /api/v1/approvals/{id}/decide": {
		summary:   "Decide an approval: approve, request changes or reject",
		request:   model.DecideApprovalInput{},
		responses: map[int]any{http.StatusOK: model.Approval{}},
	},
	"POST /api/v1/docs/{id}/reviewers": {
		summary:   "Replace a document's durable reviewer set",
		request:   model.SetDocReviewersInput{},
		responses: map[int]any{http.StatusOK: model.Doc{}},
	},
	"POST /api/v1/docs/{id}/request-approval": {
		summary:   "Open approval lanes for a document's assigned reviewers",
		responses: map[int]any{http.StatusOK: model.Doc{}},
	},
}
