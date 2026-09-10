package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// These tests cover POST /approvals/{id}/exception and the two facts the
// detail page renders beside a decision made under one (029 §7.1, 032 §7).
//
// store.SelfReviewAllowed is false for every project — no flow declares
// self-review permission — so the act refuses every call and cannot be made
// to succeed here. What is tested for real: the refusal is a 403 that leaves
// no event and no stamp, and the page renders both facts when the column
// carries an authorizer.

// exceptionForm posts the exception form the way the detail page's button
// would.
func exceptionForm(t *testing.T, h http.Handler, session string, id int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", fmt.Sprintf("/approvals/%d/exception", id), nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "wl_session", Value: session})
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestAuthorizeExceptionRefusedByPolicy: a signed-in actor with the decide
// permission asks to authorize a self-review exception, and the effective
// policy refuses. The refusal is a 403 the person can act on, and the
// transaction leaves nothing behind — no event, no stamp.
func TestAuthorizeExceptionRefusedByPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, h, iss := newOIDCServer(t, api.Config{})
	session := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana", "groups": []string{"user"},
	})
	seeded := seedPRApproval(t, st, prApprovalSeed{
		EntityID: "acme/site#71", Title: "the change", Author: "someone-else",
	})

	rr := exceptionForm(t, h, session, seeded.ID)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("exception = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "does not permit self-review") {
		t.Errorf("body does not name the policy refusal: %s", rr.Body.String())
	}
	if n := countEvents(t, st, "approval.exception_authorized"); n != 0 {
		t.Errorf("approval.exception_authorized events = %d, want 0 on a refused act", n)
	}
	a, err := st.GetApproval(ctx, seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.ExceptionAuthorizedBy != nil {
		t.Errorf("exception_authorized_by = %q after a refused act, want NULL", *a.ExceptionAuthorizedBy)
	}
}

// TestAuthorizeExceptionNeedsASession: authorizing an exception is a
// decision-grade act, so it is gated the way the decision is — an open
// instance has no identity to record as the authorizer.
func TestAuthorizeExceptionNeedsASession(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	seeded := seedAwaitingPRApproval(t, st, "acme/site#72", "the change")

	if rr := exceptionForm(t, h, "", seeded.ID); rr.Code != http.StatusForbidden {
		t.Fatalf("open-instance exception = %d, want 403", rr.Code)
	}
}

// TestApprovalDetailRendersExceptionFacts: 032 §7 asks for both facts beside
// a decision made under an exception — which flow permitted self-review, and
// who approved the exception. The column is stamped directly because the live
// policy refuses every authorization; the rendering under test is what a live
// policy would produce.
func TestApprovalDetailRendersExceptionFacts(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	seeded := seedPRApproval(t, st, prApprovalSeed{
		EntityID: "acme/site#73", Title: "the change", Author: "ada",
	})
	seedActor(t, st, "ada", "human", "Ada Lovelace", false)

	seedEvent(t, st, "exception-facts", func(tx *sql.Tx, _ int64) error {
		if err := store.SetProjectApprovalFlow(tx, seeded.ProjectID,
			model.ApprovalFlowSnapshot{
				Flow: model.ApprovalFlow{Name: "sunstone-story", Rev: "3"},
			}); err != nil {
			return err
		}
		_, err := tx.Exec(
			`UPDATE approvals SET exception_authorized_by = 'ada' WHERE id = $1`, seeded.ID)
		return err
	})

	body := getOK(t, h, fmt.Sprintf("/approvals/%d", seeded.ID))
	for _, want := range []string{
		"Self-review permitted by flow", "sunstone-story@3",
		"Self-review exception authorized by:", "Ada Lovelace",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
	// The act is offered only where the policy permits it, and none does.
	if strings.Contains(body, "/exception\"") {
		t.Error("detail page offers the exception act although no policy permits self-review")
	}
}
