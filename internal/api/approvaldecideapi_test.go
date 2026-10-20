package api_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// These tests cover POST /api/v1/approvals/{id}/decide (WL-REQ-1608).

// humanToken provisions a human actor the way a login does (groups synced
// now) and returns a bearer token for it.
func humanToken(t *testing.T, st *store.Store, id, ghLogin string, groups []string) string {
	t.Helper()
	ctx := t.Context()
	if err := st.UpsertHumanActor(ctx, id, id, false, ghLogin, "", groups); err != nil {
		t.Fatalf("upsert actor %s: %v", id, err)
	}
	token, err := st.CreateToken(ctx, id, id+" token", nil)
	if err != nil {
		t.Fatalf("create token for %s: %v", id, err)
	}
	return token
}

// setGroupsSyncedAgo moves an actor's last group sync into the past.
func setGroupsSyncedAgo(t *testing.T, st *store.Store, id, ago string) {
	t.Helper()
	seedEvent(t, st, "sync-"+id+"-"+ago, func(tx *sql.Tx, _ int64) error {
		_, err := tx.Exec(`UPDATE actors SET groups_synced_at = now() - $2::interval WHERE id = $1`, id, ago)
		return err
	})
}

func decideAPI(t *testing.T, h http.Handler, token string, id int64, verdict string) *httptest.ResponseRecorder {
	t.Helper()
	return doReq(t, h, "POST", fmt.Sprintf("/api/v1/approvals/%d/decide", id), token,
		model.DecideApprovalInput{Verdict: verdict, Note: "looked at it"})
}

func TestDecideApprovalAPIVerdicts(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	token := humanToken(t, st, "dana", "danah", []string{"user"})
	for i, tc := range []struct{ verdict, state string }{
		{"approve", "approved"},
		{"request_changes", "changes_requested"},
		{"request changes", "changes_requested"},
		{"reject", "rejected"},
	} {
		seeded := seedAwaitingPRApproval(t, st, fmt.Sprintf("acme/api#%d", i+1), "Decide over the API")
		rr := decideAPI(t, h, token, seeded.ID, tc.verdict)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s = %d, want 200; body %s", tc.verdict, rr.Code, rr.Body.String())
		}
		var got model.Approval
		decodeInto(t, rr, &got)
		if got.State != tc.state || got.ResolvingActor == nil || *got.ResolvingActor != "dana" {
			t.Errorf("%s: response state=%q resolver=%v, want %s by dana", tc.verdict, got.State, got.ResolvingActor, tc.state)
		}
		if s := approvalState(t, st, seeded.ID); s != tc.state {
			t.Errorf("%s: stored state = %q, want %s", tc.verdict, s, tc.state)
		}
	}

	seeded := seedAwaitingPRApproval(t, st, "acme/api#99", "Bad verdict")
	if rr := decideAPI(t, h, token, seeded.ID, "lgtm"); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown verdict = %d, want 422", rr.Code)
	}
	assertUntouched(t, st, seeded.ID)
}

func TestDecideApprovalAPIRefusesOwnWork(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	seeded := seedPRApproval(t, st, prApprovalSeed{EntityID: "acme/api#20", Title: "Mine", Author: "danah"})
	token := humanToken(t, st, "dana", "danah", []string{"user"})

	rr := decideAPI(t, h, token, seeded.ID, "approve")
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "you authored this change") {
		t.Fatalf("own-work decide = %d %s, want 403 naming authorship", rr.Code, rr.Body.String())
	}
	assertUntouched(t, st, seeded.ID)
}

// TestDecideApprovalAPIStaleGroups: a token whose groups were synced more than
// 24 hours ago cannot decide a role-gated row; a fresh sync can.
func TestDecideApprovalAPIStaleGroups(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	seeded := seedPRApproval(t, st, prApprovalSeed{
		EntityID: "acme/api#30", Title: "Role-gated", RequiredRole: "crew-backbone",
	})
	token := humanToken(t, st, "gina", "ginag", []string{"user", "crew-backbone"})
	setGroupsSyncedAgo(t, st, "gina", "25 hours")

	rr := decideAPI(t, h, token, seeded.ID, "approve")
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "log in again") {
		t.Fatalf("stale-groups decide = %d %s, want 403 saying log in again", rr.Code, rr.Body.String())
	}
	assertUntouched(t, st, seeded.ID)

	// A row with no role needs no group check, however stale.
	open := seedAwaitingPRApproval(t, st, "acme/api#31", "Anyone")
	if rr := decideAPI(t, h, token, open.ID, "approve"); rr.Code != http.StatusOK {
		t.Fatalf("stale groups on an unroled row = %d, want 200; body %s", rr.Code, rr.Body.String())
	}

	setGroupsSyncedAgo(t, st, "gina", "1 hour")
	if rr := decideAPI(t, h, token, seeded.ID, "approve"); rr.Code != http.StatusOK {
		t.Fatalf("fresh-groups decide = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
}

func TestDecideApprovalAPIRefusesTaskScopedToken(t *testing.T) {
	t.Parallel()
	st, h, admin := newTestServer(t)
	seeded := seedAwaitingPRApproval(t, st, "acme/api#40", "Not for agents")
	scoped := mintTaskToken(t, h, admin, seeded.TaskID, nil)

	if rr := decideAPI(t, h, scoped.Token, seeded.ID, "approve"); rr.Code != http.StatusForbidden {
		t.Fatalf("task-scoped decide = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	assertUntouched(t, st, seeded.ID)
}
