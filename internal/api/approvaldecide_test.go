package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// These tests cover POST /approvals/{id}/decide (spec 029 §7.3): who may
// decide, what a decision does, and what each refusal answers. The first is
// the property the route exists for — approving is a web-session act, so
// nothing else reaches the handler.

// sessionFor logs a person in through the full Keycloak round trip and
// returns their session cookie. claims are the ID token's, so a test states
// exactly which groups and which GitHub login the actor is provisioned with.
func sessionFor(t *testing.T, h http.Handler, iss *oidctest.Issuer, claims map[string]any) string {
	t.Helper()
	claims["aud"] = iss.ClientID
	iss.TokenClaims = claims
	return webLogin(t, h, fmt.Sprint(claims["preferred_username"]))
}

// decideForm submits the decide form the way a browser would. An empty
// session omits the cookie; headers are applied last, so a test can add
// Sec-Fetch-Site or an Authorization header.
func decideForm(t *testing.T, h http.Handler, session string, id int64,
	decision string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"decision": {decision}}
	req := httptest.NewRequest("POST", fmt.Sprintf("/approvals/%d/decide", id),
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "wl_session", Value: session})
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// approvalState reads one approval's state back out of the store.
func approvalState(t *testing.T, st *store.Store, id int64) string {
	t.Helper()
	a, err := st.GetApproval(context.Background(), id)
	if err != nil {
		t.Fatalf("get approval %d: %v", id, err)
	}
	return a.State
}

// assertUntouched checks a refused decision left the row exactly as it was:
// still awaiting, with nobody recorded as having resolved it.
func assertUntouched(t *testing.T, st *store.Store, id int64) {
	t.Helper()
	a, err := st.GetApproval(context.Background(), id)
	if err != nil {
		t.Fatalf("get approval %d: %v", id, err)
	}
	if a.State != "awaiting" {
		t.Errorf("state = %q after a refused decide, want awaiting", a.State)
	}
	if a.ResolvingActor != nil {
		t.Errorf("resolving_actor = %q after a refused decide, want NULL", *a.ResolvingActor)
	}
	if a.ResolvedAt != nil {
		t.Errorf("resolved_at is set after a refused decide")
	}
}

// TestDecideApprovalRefusesBearerAndOpenSubjects is the property spec 029
// §7.3 exists for: deciding is a web-session act. An open instance has no
// identity to attribute a decision to, and a bearer token's group claims are
// as stale as the token — neither reaches the handler, and neither moves the
// row.
func TestDecideApprovalRefusesBearerAndOpenSubjects(t *testing.T) {
	// Open instance: the authOpen subject clears webGuard but not requireSession.
	st, h, _ := newTestServer(t)
	seeded := seedAwaitingPRApproval(t, st, "acme/site#7", "Fix the widget")

	rr := doForm(t, h, fmt.Sprintf("/approvals/%d/decide", seeded.ID),
		url.Values{"decision": {"approve"}}, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("open-instance decide = %d, want 403", rr.Code)
	}
	assertUntouched(t, st, seeded.ID)

	// A perfectly valid bearer token on an instance with a login provider:
	// the web surface never reads the Authorization header, so an admin
	// token decides nothing.
	stOIDC, hOIDC, _ := newOIDCServer(t, api.Config{})
	token := seedActor(t, stOIDC, "carol", "human", "Carol", true)
	seededOIDC := seedAwaitingPRApproval(t, stOIDC, "acme/site#8", "Fix the other widget")

	rr = doForm(t, hOIDC, fmt.Sprintf("/approvals/%d/decide", seededOIDC.ID),
		url.Values{"decision": {"approve"}},
		map[string]string{"Authorization": "Bearer " + token})
	if rr.Code != http.StatusFound {
		t.Fatalf("a bearer token with no session cookie = %d, want 302 (webGuard login redirect)", rr.Code)
	}
	assertUntouched(t, stOIDC, seededOIDC.ID)
}

// TestDecideApprovalBySessionResolves is the happy path: a signed-in person
// approves, the row is resolved and attributed to them, and the mutation
// leaves exactly one event.
func TestDecideApprovalBySessionResolves(t *testing.T) {
	ctx := context.Background()
	st, h, iss := newOIDCServer(t, api.Config{})
	seeded := seedPRApproval(t, st, prApprovalSeed{
		EntityID: "acme/site#11", Title: "Ship it", Author: "octo",
	})
	session := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana",
		"groups": []string{"user"}, "github_username": "danah",
	})

	rr := decideForm(t, h, session, seeded.ID, "approve", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("decide = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != "/reviews" {
		t.Errorf("Location = %q, want /reviews", got)
	}

	a, err := st.GetApproval(ctx, seeded.ID)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if a.State != "approved" {
		t.Errorf("state = %q, want approved", a.State)
	}
	if a.ResolvingActor == nil || *a.ResolvingActor != "dana" {
		t.Errorf("resolving_actor = %v, want dana", a.ResolvingActor)
	}
	if a.ResolvedAt == nil {
		t.Error("resolved_at is NULL after a decision")
	}

	events := storeEventsOfType(t, st, "approval.decided", 1)
	if len(events) != 1 {
		t.Fatalf("approval.decided events = %d, want exactly 1", len(events))
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode event payload %s: %v", events[0].Payload, err)
	}
	if payload["decision"] != "approve" || payload["actor"] != "dana" ||
		payload["approval_id"] != float64(seeded.ID) {
		t.Errorf("event payload = %v, want the approval, the decision and the decider", payload)
	}
	if events[0].Source != "web" {
		t.Errorf("event source = %q, want web", events[0].Source)
	}
}

// TestDecideApprovalRefusesSelfApproval checks 029 §7.1's default refusal:
// the PR's own author cannot decide their change, matched on the actor's
// expected_github_login against pull_requests.author. The second half is
// what keeps this from passing for the wrong reason — the same row is
// decidable by somebody else.
func TestDecideApprovalRefusesSelfApproval(t *testing.T) {
	st, h, iss := newOIDCServer(t, api.Config{})
	seeded := seedPRApproval(t, st, prApprovalSeed{
		EntityID: "acme/site#12", Title: "My own change", Author: "danah",
	})

	author := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana",
		"groups": []string{"user"}, "github_username": "danah",
	})
	rr := decideForm(t, h, author, seeded.ID, "approve", nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("self-approval = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	assertUntouched(t, st, seeded.ID)

	other := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "erin", "name": "Erin",
		"groups": []string{"user"}, "github_username": "erinm",
	})
	if rr := decideForm(t, h, other, seeded.ID, "approve", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("a non-author decide = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	if got := approvalState(t, st, seeded.ID); got != "approved" {
		t.Errorf("state = %q after a non-author approved, want approved", got)
	}
}

// TestDecideApprovalRefusesUnqualifiedRole checks the required_role gate: a
// person whose groups do not include the approval's required role is
// refused, and one whose groups do include it is not.
func TestDecideApprovalRefusesUnqualifiedRole(t *testing.T) {
	st, h, iss := newOIDCServer(t, api.Config{})
	seeded := seedPRApproval(t, st, prApprovalSeed{
		EntityID: "acme/site#13", Title: "Needs a reviewer", RequiredRole: "crew-backbone",
	})

	outsider := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "frank", "name": "Frank",
		"groups": []string{"user"}, "github_username": "frankie",
	})
	rr := decideForm(t, h, outsider, seeded.ID, "approve", nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("unqualified decide = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	assertUntouched(t, st, seeded.ID)

	member := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "gina", "name": "Gina",
		"groups": []string{"user", "crew-backbone"}, "github_username": "ginag",
	})
	if rr := decideForm(t, h, member, seeded.ID, "approve", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("qualified decide = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	if got := approvalState(t, st, seeded.ID); got != "approved" {
		t.Errorf("state = %q after a qualified decision, want approved", got)
	}
}

// TestDecideApprovalConflictsOnResolvedRow checks the second decision on a
// row is a conflict rather than an overwrite: ResolveApproval has no state
// guard of its own, so this is the check that has to hold.
func TestDecideApprovalConflictsOnResolvedRow(t *testing.T) {
	st, h, iss := newOIDCServer(t, api.Config{})
	seeded := seedAwaitingPRApproval(t, st, "acme/site#14", "Decide me once")
	session := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana",
		"groups": []string{"user"}, "github_username": "danah",
	})

	if rr := decideForm(t, h, session, seeded.ID, "approve", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("first decide = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	rr := decideForm(t, h, session, seeded.ID, "reject", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("second decide = %d, want 409; body %s", rr.Code, rr.Body.String())
	}
	if got := approvalState(t, st, seeded.ID); got != "approved" {
		t.Errorf("state = %q after a refused second decide, want approved", got)
	}
}

// TestDecideApprovalRejectsUnknownDecision checks a decision outside the
// three the form offers is rejected before anything is written.
func TestDecideApprovalRejectsUnknownDecision(t *testing.T) {
	st, h, iss := newOIDCServer(t, api.Config{})
	seeded := seedAwaitingPRApproval(t, st, "acme/site#15", "Not frobnicable")
	session := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana",
		"groups": []string{"user"}, "github_username": "danah",
	})

	rr := decideForm(t, h, session, seeded.ID, "frobnicate", nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown decision = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	assertUntouched(t, st, seeded.ID)
}

// TestDecideApprovalRefusesCrossOrigin checks the CSRF gate: a submission a
// browser reports as cross-site is refused even carrying a valid session.
func TestDecideApprovalRefusesCrossOrigin(t *testing.T) {
	st, h, iss := newOIDCServer(t, api.Config{})
	seeded := seedAwaitingPRApproval(t, st, "acme/site#16", "Not from here")
	session := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana",
		"groups": []string{"user"}, "github_username": "danah",
	})

	rr := decideForm(t, h, session, seeded.ID, "approve",
		map[string]string{"Sec-Fetch-Site": "cross-site"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin decide = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	assertUntouched(t, st, seeded.ID)
}

// TestApprovalDecisionMetric checks worklode_approval_decisions_total carries
// one series per decision and outcome, pre-initialised to zero, and that the
// resolved and refused paths land on the labels they claim.
func TestApprovalDecisionMetric(t *testing.T) {
	st, h, admin, iss := newOIDCServerWithAdmin(t)
	gated := seedPRApproval(t, st, prApprovalSeed{
		EntityID: "acme/site#21", Title: "Reviewer only", RequiredRole: "crew-backbone",
	})
	open := seedAwaitingPRApproval(t, st, "acme/site#22", "Anyone may decide")
	session := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana",
		"groups": []string{"user"}, "github_username": "danah",
	})

	if rr := decideForm(t, h, session, gated.ID, "approve", nil); rr.Code != http.StatusForbidden {
		t.Fatalf("unqualified decide = %d, want 403", rr.Code)
	}
	// changes_requested is still an open state (029 §7.1's re-request edge),
	// so the row is decidable again; the approval after it closes the row.
	if rr := decideForm(t, h, session, open.ID, "request_changes", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("request_changes = %d, want 303", rr.Code)
	}
	if rr := decideForm(t, h, session, open.ID, "approve", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("approve after request_changes = %d, want 303", rr.Code)
	}
	if rr := decideForm(t, h, session, open.ID, "reject", nil); rr.Code != http.StatusConflict {
		t.Fatalf("decide on a resolved row = %d, want 409", rr.Code)
	}
	if rr := decideForm(t, h, session, open.ID, "frobnicate", nil); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown decision = %d, want 422", rr.Code)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	for _, want := range []string{
		`worklode_approval_decisions_total{decision="approve",outcome="refused_role"} 1`,
		`worklode_approval_decisions_total{decision="request_changes",outcome="resolved"} 1`,
		`worklode_approval_decisions_total{decision="approve",outcome="resolved"} 1`,
		`worklode_approval_decisions_total{decision="reject",outcome="conflict"} 1`,
		`worklode_approval_decisions_total{decision="invalid",outcome="invalid"} 1`,
		// Pre-initialised, so an instance where nobody has refused a
		// self-approval reads as zero rather than as no-data.
		`worklode_approval_decisions_total{decision="approve",outcome="refused_self"} 0`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}

// TestReviewsPageRendersDecideForm checks the queue row carries the control
// the route serves: a plain POST form of native submit buttons, pointed at
// this row's id, keyboard-operable with no JavaScript (032 §10).
func TestReviewsPageRendersDecideForm(t *testing.T) {
	st, h, _ := newTestServer(t)
	seeded := seedAwaitingPRApproval(t, st, "acme/site#31", "Decide me")

	body := doReq(t, h, "GET", "/reviews", "", nil).Body.String()
	bodyContains(t, body,
		fmt.Sprintf(`<form method="post" action="/approvals/%d/decide"`, seeded.ID),
		`<button type="submit" name="decision" value="approve"`,
		`<button type="submit" name="decision" value="request_changes"`,
		`<button type="submit" name="decision" value="reject"`)
	if strings.Contains(body, "hx-") || strings.Contains(body, "onclick") {
		t.Error("the decide form depends on script rather than a plain POST")
	}
}
