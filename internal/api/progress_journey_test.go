// progress_journey_test.go drives WL-SPEC-66's write journey end to end
// through a live OIDC session, the way a person clicks through the cockpit:
// log in as a spec's owner, accept its draft plan, rally the spec, confirm
// the rally, then read it back off both the page and the JSON API (§8). It
// lives here rather than in e2e/ because e2e drives public surfaces with no
// session login, and every one of these routes reads its acting actor off
// beginJSONPost's session subject (progress.go), never a bearer token's.
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// jsonPost drives one page-script write the way progress.js does: a
// same-origin JSON POST carrying the X-Requested-With header beginJSONPost
// requires (WL-SPEC-66 §4.2), plus the caller's session cookie.
func jsonPost(t *testing.T, h http.Handler, path, session, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "lode-cockpit")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "wl_session", Value: session})
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestProgressWriteJourney is WL-SPEC-66 §8's write journey: the owner logs
// in, accepts their draft plan, assembles a rally around the spec it covers,
// confirms it, and sees the rally active from both the page and the API.
func TestProgressWriteJourney(t *testing.T) {
	t.Parallel()
	st, h, iss := newOIDCServer(t, api.Config{})
	createProject(t, st, "proj")

	session := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana",
		"groups": []string{"user"}, "github_username": "danah",
	})
	// The login above provisioned "dana" as a human actor; a bearer token for
	// the same actor is what creates the documents she then owns and accepts,
	// and what reads the JSON rally back — /api/v1 authenticates by
	// Authorization header only, never a session cookie.
	token, err := st.CreateToken(context.Background(), "dana", "journey token", nil)
	if err != nil {
		t.Fatalf("create token for dana: %v", err)
	}

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 66, Slug: "066-progress",
		Body: progressSpecBody,
	})
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "066-progress-plan",
		Body: progressPlanBody,
	})

	// 1. Accept the draft plan through the page's route.
	rr := jsonPost(t, h, "/projects/proj/progress/accept", session,
		`{"doc":`+strconv.FormatInt(plan.ID, 10)+`}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept = %d, body %s", rr.Code, rr.Body.String())
	}
	var accepted model.ProgressAcceptResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accept response %s: %v", rr.Body.String(), err)
	}
	if accepted.Status != "accepted" || accepted.Minted != 2 {
		t.Fatalf("accept response = %+v, want status accepted and 2 minted tasks", accepted)
	}

	// 2. Rally the spec: both minted tasks join a new draft rally.
	rr = jsonPost(t, h, "/projects/proj/progress/rally/add", session,
		`{"doc":`+strconv.FormatInt(spec.ID, 10)+`}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("rally/add = %d, body %s", rr.Code, rr.Body.String())
	}
	var added model.ProgressRallyAddResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &added); err != nil {
		t.Fatalf("decode rally/add response %s: %v", rr.Body.String(), err)
	}
	if added.Added != 2 || added.Members != 2 || added.Specs != 1 || added.Rally == "" {
		t.Fatalf("rally/add response = %+v, want 2 added, 2 members, 1 spec, a rally id", added)
	}

	// 3. Confirm it: draft -> ready, the state that makes it the project's
	// one active rally.
	rr = jsonPost(t, h, "/projects/proj/progress/rally/confirm", session, `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("rally/confirm = %d, body %s", rr.Code, rr.Body.String())
	}
	var confirmed model.ProgressRallyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &confirmed); err != nil {
		t.Fatalf("decode rally/confirm response %s: %v", rr.Body.String(), err)
	}
	if confirmed.Rally != added.Rally || confirmed.State != "ready" {
		t.Fatalf("rally/confirm response = %+v, want %s in state ready", confirmed, added.Rally)
	}

	// 4. The page, read as the session that just confirmed it, shows the
	// rally band active rather than "No active rally".
	page := withSession(t, h, "GET", "/projects/proj/progress", session, "")
	if page.Code != http.StatusOK {
		t.Fatalf("GET progress page = %d, body %s", page.Code, page.Body.String())
	}
	if body := page.Body.String(); !strings.Contains(body, `href="/tasks/`+confirmed.Rally+`"`) {
		t.Errorf("progress page does not show the active rally %s:\n%s", confirmed.Rally, body)
	}

	// 5. GET /api/v1/projects/{id}/rally agrees: the confirmed task, active.
	rr = doReq(t, h, "GET", "/api/v1/projects/proj/rally", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET rally = %d, body %s", rr.Code, rr.Body.String())
	}
	var rally model.Rally
	if err := json.Unmarshal(rr.Body.Bytes(), &rally); err != nil {
		t.Fatalf("decode rally %s: %v", rr.Body.String(), err)
	}
	if rally.Task.ID != confirmed.Rally || rally.Task.State != "ready" {
		t.Fatalf("GET rally = %+v, want task %s in state ready", rally.Task, confirmed.Rally)
	}
}
