package api_test

import (
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestClauseEdgesAPI: link, read the edge on the target's detail, repeat is
// 409, bad type is 422, unknown target is 404, malformed target is 400,
// unlink is 204 and absent unlink is 404. Also checks the recorded
// clause.linked event names the from clause, not just the request body (I1).
func TestClauseEdgesAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1,
	}) // WL-CL-1..3

	body := model.ClauseEdgeInput{Type: "constrains", To: "WL-CL-3"}
	rr := doReq(t, h, http.MethodPost, "/api/v1/clauses/WL-CL-1/edges", token, body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("link: %d %s", rr.Code, rr.Body)
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/clauses/WL-CL-1/edges", token, body)
	if rr.Code != http.StatusConflict {
		t.Errorf("repeat link: %d", rr.Code)
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/clauses/WL-CL-1/edges", token, model.ClauseEdgeInput{Type: "amends", To: "WL-CL-3"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad type: %d", rr.Code)
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/clauses/WL-CL-1/edges", token, model.ClauseEdgeInput{Type: "refines", To: "WL-CL-999"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown target: %d", rr.Code)
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/clauses/WL-CL-1/edges", token, model.ClauseEdgeInput{Type: "refines", To: "nope"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed target: %d", rr.Code)
	}

	var c model.Clause
	rr = doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-3", token, nil)
	decodeInto(t, rr, &c)
	if len(c.Edges) != 1 || c.Edges[0].From != "WL-CL-1" || c.Edges[0].Type != "constrains" {
		t.Errorf("edges on target: %+v", c.Edges)
	}

	events := pollEvents(t, h, token, "?type=clause.linked", 1)
	checkPayloadProps(t, eventPayload(t, events[0].(map[string]any)), map[string]string{
		"from": "WL-CL-1", "to": "WL-CL-3", "type": "constrains",
	})

	rr = doReq(t, h, http.MethodDelete, "/api/v1/clauses/WL-CL-1/edges", token, body)
	if rr.Code != http.StatusNoContent {
		t.Errorf("unlink: %d %s", rr.Code, rr.Body)
	}
	rr = doReq(t, h, http.MethodDelete, "/api/v1/clauses/WL-CL-1/edges", token, body)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unlink absent: %d", rr.Code)
	}
}
