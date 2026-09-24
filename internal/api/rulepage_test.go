package api_test

// rulepage_test.go covers the cockpit rule page and the S20 canonical
// URLs and redirects: GET /projects/{proj}/rule/{n}, its version sibling,
// the project-key redirect, and the /rules/{ref} resolving redirect.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestRulePageAndRedirects covers S20's rule page and its redirects:
// an uppercase project key, and the /rules/{ref} resolving redirect.
func TestRulePageAndRedirects(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: ruleDocV1})

	rr := doReq(t, h, http.MethodGet, "/projects/"+projID+"/rule/2", "", nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "WL-RULE-2") || !strings.Contains(rr.Body.String(), "Sub") {
		t.Errorf("rule page = %d\n%s", rr.Code, rr.Body.String()[:min(400, rr.Body.Len())])
	}
	if strings.Contains(rr.Body.String(), "back to current") {
		t.Errorf("the current rule page carries the older-version banner")
	}
	rr = doReq(t, h, http.MethodGet, "/projects/"+projID+"/rule/2/1", "", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("rule version page = %d", rr.Code)
	}
	for path, want := range map[string]string{
		"/rules/WL-RULE-2":    "/projects/" + projID + "/rule/2",
		"/projects/WL/rule/2": "/projects/" + projID + "/rule/2",
	} {
		rr := doReq(t, h, http.MethodGet, path, "", nil)
		if rr.Code != http.StatusFound || rr.Header().Get("Location") != want {
			t.Errorf("%s = %d %q, want 302 %q", path, rr.Code, rr.Header().Get("Location"), want)
		}
	}
	for _, path := range []string{
		"/projects/" + projID + "/rule/99",
		"/projects/" + projID + "/feature/1",
		"/rules/nope",
		"/projects/nowhere/rule/2",
	} {
		if rr := doReq(t, h, http.MethodGet, path, "", nil); rr.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rr.Code)
		}
	}
}

// TestRulePageOlderVersionLinksBack: an older version's page says which
// version it is showing and links back to the current one, the way a
// document version's page does.
func TestRulePageOlderVersionLinksBack(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	doc := createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: ruleDocV1})
	acceptDocViaAPI(t, h, token, doc.ID)
	rr := doReq(t, h, http.MethodPut, "/api/v1/rules/WL-RULE-2", token,
		model.EditRuleInput{Heading: "Sub", Body: "\nB changed.\n\n"})
	if rr.Code != http.StatusOK {
		t.Fatalf("edit rule = %d %s", rr.Code, rr.Body)
	}
	if rr := doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/docs/%d/revision/accept", doc.ID), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("accept revision = %d %s", rr.Code, rr.Body)
	}

	rr = doReq(t, h, http.MethodGet, "/projects/"+projID+"/rule/2/1", "", nil)
	body := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(body, "Viewing version 1") ||
		!strings.Contains(body, `href="/projects/`+projID+`/rule/2"`) {
		t.Errorf("version 1 page = %d, want the back-to-current banner\n%s", rr.Code, body[:min(1200, len(body))])
	}
	rr = doReq(t, h, http.MethodGet, "/projects/"+projID+"/rule/2/2", "", nil)
	if strings.Contains(rr.Body.String(), "back to current") {
		t.Errorf("the current version's own page carries the banner")
	}
}

// TestRulePageUppercaseProjectID: createProject checks the shape of the
// key and only that the id is non-empty, so an uppercase project id is
// legal. Choosing the lookup from the case of {proj} routed those to
// ProjectByKey, which no key can match, and the page was unreachable.
func TestRulePageUppercaseProjectID(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	if err := st.CreateProject(context.Background(), "MyProj", "MyProj", "MP"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	createDocViaAPI(t, h, token, model.CreateDocInput{Project: "MyProj", Kind: "spec", Slug: "t", Body: ruleDocV1})
	rr := doReq(t, h, http.MethodGet, "/projects/MyProj/rule/2", "", nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "MP-RULE-2") {
		t.Errorf("rule page for the uppercase project id MyProj = %d, want 200 with MP-RULE-2", rr.Code)
	}
}

// TestRulePageShowsOwnerTagsAndEdges covers task 8: the owner chip, tag
// chips and the Edges card on the rule page, plus the card's honest empty
// state when a rule has no edges.
func TestRulePageShowsOwnerTagsAndEdges(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: ruleDocV1}) // WL-RULE-1..3

	rr := doReq(t, h, http.MethodPost, "/api/v1/rules/WL-RULE-1/edges", token, model.RuleEdgeInput{Type: "constrains", To: "WL-RULE-3"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("link: %d %s", rr.Code, rr.Body)
	}
	rr = doReq(t, h, http.MethodPatch, "/api/v1/rules/WL-RULE-3", token,
		model.RuleMetaInput{Owner: strPtr("stig"), Tags: &[]string{"security"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("set meta: %d %s", rr.Code, rr.Body)
	}

	rr = doReq(t, h, http.MethodGet, "/projects/"+projID+"/rule/3", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("rule page = %d %s", rr.Code, rr.Body)
	}
	body := rr.Body.String()
	for _, want := range []string{"constrains", "WL-RULE-1", "owner stig", "security"} {
		if !strings.Contains(body, want) {
			t.Errorf("rule page missing %q:\n%s", want, body)
		}
	}

	// WL-RULE-2 has no edges: the card says so rather than rendering empty.
	rr = doReq(t, h, http.MethodGet, "/projects/"+projID+"/rule/2", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("rule page = %d %s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "No edges.") {
		t.Errorf("rule page missing empty edges state:\n%s", rr.Body.String())
	}
}
