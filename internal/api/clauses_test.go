package api_test

import (
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// clauseDocV1 mirrors internal/store/clauses_test.go's fixture of the same
// name: a spec whose first anchored section mints WL-CL-1.
const clauseDocV1 = "---\nstatus: draft\n---\n# T\n\nIntro.\n\n## 1. One {#sec-1}\n\nA.\n\n### 1.1 Sub {#sec-1.1}\n\nB.\n\n## 2. Two {#sec-2}\n\nC.\n"

// TestGetClause reads a clause over the API by its ref, and answers 400 for
// a malformed ref and 404 for one that doesn't resolve.
func TestGetClause(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1,
	})

	rr := doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var c model.Clause
	decodeInto(t, rr, &c)
	if c.Ref != "WL-CL-1" || c.Heading != "One" {
		t.Errorf("clause = %+v", c)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/clauses/nonsense", token, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("malformed ref: status = %d", rr.Code)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-999", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("missing clause: status = %d", rr.Code)
	}
}
