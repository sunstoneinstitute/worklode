package api_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ruleDocV1 mirrors internal/store/rules_test.go's fixture of the same
// name: a spec whose first anchored section mints WL-RULE-1.
const ruleDocV1 = "---\nstatus: draft\n---\n# T\n\nIntro.\n\n## 1. One {#sec-1}\n\nA.\n\n### 1.1 Sub {#sec-1.1}\n\nB.\n\n## 2. Two {#sec-2}\n\nC.\n"

// TestGetRule reads a rule over the API by its ref, and answers 400 for
// a malformed ref and 404 for one that doesn't resolve.
func TestGetRule(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: projID, Kind: "spec", Slug: "t", Body: ruleDocV1,
	})

	rr := doReq(t, h, http.MethodGet, "/api/v1/rules/WL-RULE-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var c model.Rule
	decodeInto(t, rr, &c)
	if c.Ref != "WL-RULE-1" || c.Heading != "One" {
		t.Errorf("rule = %+v", c)
	}
	// The pre-S64 spelling WL-CL-1 names the same rule (S64).
	rr = doReq(t, h, http.MethodGet, "/api/v1/rules/WL-CL-1", token, nil)
	var alias model.Rule
	decodeInto(t, rr, &alias)
	if rr.Code != http.StatusOK || alias.Ref != "WL-RULE-1" {
		t.Errorf("WL-CL-1: status %d, rule %+v; want WL-RULE-1", rr.Code, alias)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/rules/nonsense", token, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("malformed ref: status = %d", rr.Code)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/rules/WL-RULE-999", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("missing rule: status = %d", rr.Code)
	}
}

// TestRuleEditAndVersionsAPI edits a rule through the API on a draft
// document, again after acceptance (landing through the revision path), then
// reads its version history back (S14, S35).
func TestRuleEditAndVersionsAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	doc := createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: ruleDocV1})

	rr := doReq(t, h, http.MethodPut, "/api/v1/rules/WL-RULE-2", token, model.EditRuleInput{Heading: "Subsection", Body: "\nB changed.\n\n"})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rr.Code, rr.Body)
	}
	var c model.Rule
	decodeInto(t, rr, &c)
	if c.Heading != "Subsection" || c.Version != 1 || c.Body != "\nB changed.\n\n" {
		t.Errorf("edited rule = %+v", c)
	}
	rr = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/docs/%d", doc.ID), token, nil)
	var d model.DocDetail
	decodeInto(t, rr, &d)
	if !strings.Contains(d.Body, "### 1.1 Subsection {#sec-1.1}\n\nB changed.\n") {
		t.Errorf("doc body not regenerated:\n%s", d.Body)
	}

	acceptDocViaAPI(t, h, token, doc.ID)
	rr = doReq(t, h, http.MethodPut, "/api/v1/rules/WL-RULE-2", token, model.EditRuleInput{Heading: "Subsection", Body: "\nB again.\n\n"})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT on accepted = %d %s", rr.Code, rr.Body)
	}
	decodeInto(t, rr, &c)
	if c.Body != "\nB changed.\n\n" || c.Status != "accepted" {
		t.Errorf("accepted rule moved before the revision landed: %+v", c)
	}
	rr = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/docs/%d/revision/accept", doc.ID), token, nil)
	if rr.Code/100 != 2 {
		t.Fatalf("accept revision = %d %s", rr.Code, rr.Body)
	}

	rr = doReq(t, h, http.MethodGet, "/api/v1/rules/WL-RULE-2/versions", token, nil)
	var vs []model.RuleVersion
	decodeInto(t, rr, &vs)
	if rr.Code != http.StatusOK || len(vs) != 2 || vs[0].Version != 2 {
		t.Errorf("versions = %d %+v", rr.Code, vs)
	}
	rr = doReq(t, h, http.MethodGet, "/api/v1/rules/WL-RULE-2/versions/1", token, nil)
	decodeInto(t, rr, &c)
	if rr.Code != http.StatusOK || c.Version != 1 || c.Body != "\nB changed.\n\n" {
		t.Errorf("v1 = %d %+v", rr.Code, c)
	}
	rr = doReq(t, h, http.MethodGet, "/api/v1/rules/WL-RULE-2/versions/2", token, nil)
	decodeInto(t, rr, &c)
	if rr.Code != http.StatusOK || c.Version != 2 || c.Body != "\nB again.\n\n" {
		t.Errorf("v2 = %d %+v", rr.Code, c)
	}

	for _, tc := range []struct {
		method, path string
		body         any
		want         int
	}{
		{http.MethodPut, "/api/v1/rules/WL-RULE-99", model.EditRuleInput{Heading: "x", Body: "y"}, http.StatusNotFound},
		{http.MethodPut, "/api/v1/rules/nope", model.EditRuleInput{Heading: "x", Body: "y"}, http.StatusBadRequest},
		{http.MethodPut, "/api/v1/rules/WL-RULE-2", model.EditRuleInput{Heading: "", Body: "y"}, http.StatusUnprocessableEntity},
		{http.MethodGet, "/api/v1/rules/WL-RULE-2/versions/9", nil, http.StatusNotFound},
		{http.MethodGet, "/api/v1/rules/WL-RULE-2/versions/x", nil, http.StatusBadRequest},
		{http.MethodGet, "/api/v1/rules/WL-RULE-99/versions", nil, http.StatusNotFound},
	} {
		rr := doReq(t, h, tc.method, tc.path, token, tc.body)
		if rr.Code != tc.want {
			t.Errorf("%s %s = %d, want %d: %s", tc.method, tc.path, rr.Code, tc.want, rr.Body)
		}
	}
}

// TestRuleMetaAPI sets owner and tags through PATCH, checks the recorded
// rule.updated event names the patched rule (not just the request body,
// I1 of the Task 5 pattern), refuses an empty body with 422, and answers 404
// for an unknown rule (S15).
func TestRuleMetaAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: ruleDocV1})

	rr := doReq(t, h, http.MethodPatch, "/api/v1/rules/WL-RULE-1", token,
		model.RuleMetaInput{Owner: strPtr("stig"), Tags: &[]string{"a", "b"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", rr.Code, rr.Body)
	}
	var c model.Rule
	decodeInto(t, rr, &c)
	if c.Owner != "stig" || len(c.Tags) != 2 || c.Tags[0] != "a" || c.Tags[1] != "b" {
		t.Errorf("patched rule = %+v", c)
	}

	events := pollEvents(t, h, token, "?type=rule.updated", 1)
	checkPayloadProps(t, eventPayload(t, events[0].(map[string]any)), map[string]string{"rule": "WL-RULE-1"})

	if rr := doReq(t, h, http.MethodPatch, "/api/v1/rules/WL-RULE-1", token, model.RuleMetaInput{}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty body: status = %d, body %s", rr.Code, rr.Body)
	}
	if rr := doReq(t, h, http.MethodPatch, "/api/v1/rules/WL-RULE-999", token, model.RuleMetaInput{Owner: strPtr("stig")}); rr.Code != http.StatusNotFound {
		t.Errorf("unknown rule: status = %d", rr.Code)
	}
}

// TestListRules lists a document's arrangement by its WL-SPEC ref, in
// order, and answers 404 for an unknown document and 422 for a bad status.
func TestListRules(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: projID, Kind: "spec", Slug: "t", Body: ruleDocV1,
	})

	rr := doReq(t, h, http.MethodGet, "/api/v1/rules?doc=WL-SPEC-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var cs []model.Rule
	decodeInto(t, rr, &cs)
	if len(cs) != 3 || cs[0].Ref != "WL-RULE-1" || cs[1].Heading != "Sub" || cs[2].Heading != "Two" {
		t.Errorf("rules = %+v", cs)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/rules?doc=WL-SPEC-99", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown doc: status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/rules?status=bogus", token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad status: status = %d", rr.Code)
	}
}
