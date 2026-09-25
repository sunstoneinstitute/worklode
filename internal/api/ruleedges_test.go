package api_test

import (
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestRuleEdgesAPI: link, read the edge on the target's detail, repeat is
// 409, bad type is 422, unknown target is 404, malformed target is 400,
// unlink is 204 and absent unlink is 404. Also checks the recorded
// rule.linked event names the from rule, not just the request body (I1).
func TestRuleEdgesAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: projID, Kind: "spec", Slug: "t", Body: ruleDocV1,
	}) // WL-RULE-1..3

	body := model.RuleEdgeInput{Type: "constrains", To: "WL-RULE-3"}
	rr := doReq(t, h, http.MethodPost, "/api/v1/rules/WL-RULE-1/edges", token, body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("link: %d %s", rr.Code, rr.Body)
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/rules/WL-RULE-1/edges", token, body)
	if rr.Code != http.StatusConflict {
		t.Errorf("repeat link: %d", rr.Code)
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/rules/WL-RULE-1/edges", token, model.RuleEdgeInput{Type: "bogus", To: "WL-RULE-3"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad type: %d", rr.Code)
	}
	// supersedes has one writer, lode rule supersede; its inverse is never stored.
	for _, typ := range []string{"supersedes", "supersededBy"} {
		rr = doReq(t, h, http.MethodPost, "/api/v1/rules/WL-RULE-1/edges", token, model.RuleEdgeInput{Type: typ, To: "WL-RULE-3"})
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s over the edges API: %d, want 422", typ, rr.Code)
		}
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/rules/WL-RULE-1/edges", token, model.RuleEdgeInput{Type: "refines", To: "WL-RULE-999"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown target: %d", rr.Code)
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/rules/WL-RULE-1/edges", token, model.RuleEdgeInput{Type: "refines", To: "nope"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed target: %d", rr.Code)
	}

	var c model.Rule
	rr = doReq(t, h, http.MethodGet, "/api/v1/rules/WL-RULE-3", token, nil)
	decodeInto(t, rr, &c)
	if len(c.Edges) != 1 || c.Edges[0].From != "WL-RULE-1" || c.Edges[0].Type != "constrains" {
		t.Errorf("edges on target: %+v", c.Edges)
	}

	events := pollEvents(t, h, token, "?type=rule.linked", 1)
	checkPayloadProps(t, eventPayload(t, events[0].(map[string]any)), map[string]string{
		"from": "WL-RULE-1", "to": "WL-RULE-3", "type": "constrains",
	})

	rr = doReq(t, h, http.MethodDelete, "/api/v1/rules/WL-RULE-1/edges", token, body)
	if rr.Code != http.StatusNoContent {
		t.Errorf("unlink: %d %s", rr.Code, rr.Body)
	}
	rr = doReq(t, h, http.MethodDelete, "/api/v1/rules/WL-RULE-1/edges", token, body)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unlink absent: %d", rr.Code)
	}

	// amends is a manual edge: written and removed over the same path.
	amends := model.RuleEdgeInput{Type: "amends", To: "WL-RULE-1"}
	if rr = doReq(t, h, http.MethodPost, "/api/v1/rules/WL-RULE-3/edges", token, amends); rr.Code != http.StatusCreated {
		t.Errorf("link amends: %d %s", rr.Code, rr.Body)
	}
	if rr = doReq(t, h, http.MethodDelete, "/api/v1/rules/WL-RULE-3/edges", token, amends); rr.Code != http.StatusNoContent {
		t.Errorf("unlink amends: %d %s", rr.Code, rr.Body)
	}
}
