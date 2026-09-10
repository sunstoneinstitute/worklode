package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// These tests cover the impact review lifecycle's web half (029 §7.1): the
// dependent owner's note on POST /approvals/{id}/note, and the prior
// approver's decision through the existing decide route.

// impactSeedSeq keeps two seeds in one test off each other's entity ids.
var impactSeedSeq atomic.Int64

// seedImpactApproval drives the real fan-out to produce one open impact row:
// a dependent PR that approver has already approved, a governed reference
// from it to an upstream PR, and a designation of a new upstream head. It
// returns the impact row's id and the dependent's entity id.
//
// approver must be an actor the store already knows — log in first, then
// seed, so the approved row can name them. "" leaves the approval attributed
// to nobody, which is all a test that never gets past requireSession needs.
func seedImpactApproval(t *testing.T, st *store.Store, approver string) (int64, string) {
	t.Helper()
	n := impactSeedSeq.Add(1)
	depEntity := fmt.Sprintf("acme/dep#%d", 500+n)
	upEntity := fmt.Sprintf("acme/up#%d", 500+n)
	dep := seedPRApproval(t, st, prApprovalSeed{EntityID: depEntity, Title: "the dependent"})

	seedEvent(t, st, fmt.Sprintf("impact-seed-%d", n), func(tx *sql.Tx, _ int64) error {
		now := st.Now()
		var by *string
		if approver != "" {
			by = &approver
		}
		if err := store.ResolveApproval(tx, dep.ID, "approved", by, now); err != nil {
			return err
		}
		if _, err := store.InsertAwaitingApproval(tx, now, "pr", upEntity, "up-a",
			"", nil, nil, nil); err != nil {
			return err
		}
		if err := store.InsertGovernedRefs(tx, now, nil, "pr", depEntity, "up-a",
			[]store.GovernedRef{{Kind: "pr", ID: upEntity, Revision: "up-a"}}); err != nil {
			return err
		}
		_, opened, err := store.DesignateRevision(tx, now, "pr", upEntity, "up-b")
		if err != nil {
			return err
		}
		if opened != 1 {
			return fmt.Errorf("designation opened %d impact rows, want 1", opened)
		}
		return nil
	})

	rows, err := st.ListApprovalsForEntityCtx(context.Background(), "pr", depEntity)
	if err != nil {
		t.Fatalf("approval history for %s: %v", depEntity, err)
	}
	for _, r := range rows {
		if r.ReviewKind == "impact" {
			return r.ID, depEntity
		}
	}
	t.Fatalf("no impact row on %s after the designation", depEntity)
	return 0, ""
}

// noteForm submits the impact-note form the way a browser would.
func noteForm(t *testing.T, h http.Handler, session string, id int64, note string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"note": {note}}
	req := httptest.NewRequest("POST", fmt.Sprintf("/approvals/%d/note", id),
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "wl_session", Value: session})
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestImpactNotePersists: the dependent's owner records what the upstream
// change means, the page they land back on is the one that shows it, and the
// act leaves exactly one event.
func TestImpactNotePersists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, h, iss := newOIDCServer(t, api.Config{})
	session := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana", "groups": []string{"user"},
	})
	id, _ := seedImpactApproval(t, st, "dana")

	rr := noteForm(t, h, session, id, "the schema change does not reach us")
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("note = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	if got, want := rr.Header().Get("Location"), fmt.Sprintf("/approvals/%d", id); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	a, err := st.GetApproval(ctx, id)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if a.Note == nil || *a.Note != "the schema change does not reach us" {
		t.Errorf("note = %v, want the submitted text", a.Note)
	}

	events := storeEventsOfType(t, st, "approval.impact_noted", 1)
	if len(events) != 1 {
		t.Fatalf("approval.impact_noted events = %d, want exactly 1", len(events))
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode event payload %s: %v", events[0].Payload, err)
	}
	if payload["actor"] != "dana" || payload["approval_id"] != float64(id) {
		t.Errorf("event payload = %v, want the approval and the actor", payload)
	}
	if events[0].Source != "web" {
		t.Errorf("event source = %q, want web", events[0].Source)
	}
}

// TestImpactNoteRefusesWithoutSession: the note is a web-session act for the
// same reason the decision is — an open instance has no identity to attribute
// it to, so nothing is written.
func TestImpactNoteRefusesWithoutSession(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	id, _ := seedImpactApproval(t, st, "")

	rr := doForm(t, h, fmt.Sprintf("/approvals/%d/note", id),
		url.Values{"note": {"anonymous"}}, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("open-instance note = %d, want 403", rr.Code)
	}
	a, err := st.GetApproval(context.Background(), id)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if a.Note != nil {
		t.Errorf("note = %q after a refused act, want NULL", *a.Note)
	}
}

// TestImpactDecideByPriorApprover: the impact question goes to whoever
// approved the dependent (029 §7.1) and travels the ordinary decide route.
// Anyone else is refused, which is what keeps the first half from passing
// for the wrong reason.
func TestImpactDecideByPriorApprover(t *testing.T) {
	t.Parallel()
	st, h, iss := newOIDCServer(t, api.Config{})
	approver := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana", "groups": []string{"user"},
	})
	other := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "erin", "name": "Erin", "groups": []string{"user"},
	})
	id, _ := seedImpactApproval(t, st, "dana")

	if rr := decideForm(t, h, other, id, "approve", nil); rr.Code != http.StatusForbidden {
		t.Fatalf("a non-prior-approver decide = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	assertUntouched(t, st, id)

	rr := decideForm(t, h, approver, id, "approve", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("prior-approver decide = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	if got := approvalState(t, st, id); got != "approved" {
		t.Errorf("impact state = %q, want approved", got)
	}
}
