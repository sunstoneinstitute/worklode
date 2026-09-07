package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// RequestDocApproval calls POST /api/v1/docs/{id}/request-approval, opening
// one awaiting lane per reviewer in the document's durable reviewer set (025
// §7.3; assigned separately, see SetDocReviewers) on its current version. A
// document with no reviewers assigned is a 422 naming it. Re-requesting the
// same set at the same version changes nothing.
//
// There is deliberately no Decide counterpart on this client: 029 §7.3 makes
// approving a web UI act, because a session's group claims are fresh and a
// 30-day CLI token's are not.
func (c *Client) RequestDocApproval(ctx context.Context, id int64) (model.Doc, []byte, error) {
	return doJSON[model.Doc](ctx, c, http.MethodPost,
		"/api/v1/docs/"+strconv.FormatInt(id, 10)+"/request-approval",
		nil, "doc")
}

// ListApprovals calls GET /api/v1/approvals: every outstanding approval,
// oldest first, across entity kinds and projects.
func (c *Client) ListApprovals(ctx context.Context) (model.ApprovalListResponse, []byte, error) {
	return doJSON[model.ApprovalListResponse](ctx, c, http.MethodGet, "/api/v1/approvals", nil, "approval list")
}

// RequireApproval calls POST /api/v1/approvals: 029 §7.2's ad-hoc
// requirement on any governed target. Re-filing the same
// (kind, id, revision, lane) returns the row already there, so this is safe
// to repeat.
func (c *Client) RequireApproval(ctx context.Context, in model.RequireApprovalInput) (model.Approval, []byte, error) {
	return doJSON[model.Approval](ctx, c, http.MethodPost, "/api/v1/approvals", in, "approval")
}

// ApprovalRender writes one approval in full: what it governs, which lane,
// what state it is in, and who owes the decision.
func ApprovalRender(w io.Writer, a model.Approval) {
	fmt.Fprintf(w, "%s %s\n", a.EntityKind, a.EntityID)
	tw := newTabwriter(w)
	fmt.Fprintf(tw, "  approval:\t%d\n", a.ID)
	if a.SubjectRevision != "" {
		fmt.Fprintf(tw, "  revision:\t%s\n", a.SubjectRevision)
	}
	if a.Lane != "" {
		fmt.Fprintf(tw, "  lane:\t%s\n", a.Lane)
	}
	fmt.Fprintf(tw, "  state:\t%s\n", a.State)
	fmt.Fprintf(tw, "  awaiting:\t%s\n", awaits(a.RequiredActor, a.RequiredRole))
	fmt.Fprintf(tw, "  requested:\t%s by %s\n", LocalTime(a.CreatedAt), awaits(a.CreatedBy, nil))
	if a.ResolvedAt != nil {
		fmt.Fprintf(tw, "  resolved:\t%s by %s\n", LocalTime(*a.ResolvedAt), awaits(a.ResolvingActor, nil))
	}
	tw.Flush()
}

// ApprovalTable prints one row per awaiting approval: the id the cockpit's
// decide form takes, the entity kind, the reviewer it waits on, the project,
// how long it has waited, and the title.
//
// The title comes last and is the wrapping column, as in DocTable: it is the
// widest value and the one a reader scans for. Who it awaits falls back to
// the actor id when the actor has no display name, and to the required role
// when the lane names a group rather than a person — a lane naming neither
// (an unqualified PR lane) renders "-".
func ApprovalTable(w io.Writer, rows []model.AwaitingApproval) {
	tbl := newTable(
		column{header: "ID"},
		column{header: "KIND"},
		holderColumn("AWAITING"),
		column{header: "PROJECT"},
		column{header: "REQUESTED"},
		titleColumn("TITLE"),
	)
	for _, a := range rows {
		tbl.add(strconv.FormatInt(a.ID, 10), a.EntityKind, approvalAwaits(a),
			dash(a.Project), LocalTime(a.CreatedAt), a.Title)
	}
	tbl.flush(w)
}

// approvalAwaits renders who a lane is waiting on, preferring a display name
// over the raw actor id and falling back to the required role.
func approvalAwaits(a model.AwaitingApproval) string {
	if a.RequiredActorName != nil && *a.RequiredActorName != "" {
		return *a.RequiredActorName
	}
	return awaits(a.RequiredActor, a.RequiredRole)
}

// awaits is approvalAwaits over the bare approval row, which carries no
// display name: the actor, else the role, else "-". Passing a nil role reads
// one nullable actor column on its own.
func awaits(actor, role *string) string {
	if actor != nil && *actor != "" {
		return *actor
	}
	if role != nil && *role != "" {
		return *role
	}
	return "-"
}
