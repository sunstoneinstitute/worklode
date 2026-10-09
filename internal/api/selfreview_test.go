package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestSoloOwnerDecidesOwnSpec is WL-944: the owner of a solo project wrote a
// spec (through an agent running under their credentials) and is its only
// reviewer. Without a flow the decision is refused; once the project carries
// the solo flow, the same person approves it through the web act and the row
// records the self-review.
func TestSoloOwnerDecidesOwnSpec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, h, iss := newOIDCServer(t, api.Config{})
	token := seedActor(t, st, "dana", "human", "Dana", false)
	admin := seedActor(t, st, "root", "human", "Root", true)
	createProject(t, st, "soloproj")
	d := draftSpecForReview(t, h, token, "soloproj", "solo-spec", 71)
	setReviewers(t, h, token, d.ID, []string{"dana"})
	if rr := doReq(t, h, "POST", docPath(d.ID, "/request-approval"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("request approval = %d, body %s", rr.Code, rr.Body.String())
	}
	rows := awaitingFor(t, st, d.ID)
	if len(rows) != 1 {
		t.Fatalf("awaiting rows = %d, want 1", len(rows))
	}
	id := rows[0].ID
	session := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana", "groups": []string{"user"},
	})

	if rr := decideForm(t, h, session, id, "approve", nil); rr.Code != http.StatusForbidden {
		t.Fatalf("self-decide without a flow = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	assertUntouched(t, st, id)

	if rr := doReq(t, h, "POST", "/api/v1/projects/soloproj/approval-flow", admin,
		model.ApplyApprovalFlowInput{Name: "solo"}); rr.Code != http.StatusOK {
		t.Fatalf("apply solo flow = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := decideForm(t, h, session, id, "approve", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("self-decide under solo = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	a, err := st.GetApproval(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if a.State != "approved" {
		t.Errorf("state = %q, want approved", a.State)
	}
	if a.ExceptionAuthorizedBy == nil || *a.ExceptionAuthorizedBy != "dana" {
		t.Errorf("exception_authorized_by = %v, want dana", a.ExceptionAuthorizedBy)
	}

	body := withSession(t, h, "GET", fmt.Sprintf("/approvals/%d", id), session, "").Body.String()
	for _, want := range []string{"Self-review permitted by flow", "solo@1", "Self-reviewed by:"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
}

// TestApprovalDetailRendersSelfReviewFacts: WL-SPEC-82 §9 asks for both facts beside
// a self-reviewed decision — which flow permitted it, and who reviewed.
func TestApprovalDetailRendersSelfReviewFacts(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	seeded := seedPRApproval(t, st, prApprovalSeed{
		EntityID: "acme/site#73", Title: "the change", Author: "ada",
	})
	seedActor(t, st, "ada", "human", "Ada Lovelace", false)

	seedEvent(t, st, "self-review-facts", func(tx *sql.Tx, _ int64) error {
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
		"Self-reviewed by:", "Ada Lovelace",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
}
