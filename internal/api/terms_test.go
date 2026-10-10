package api_test

// terms_test.go covers definition terms (WL-SPEC-77 §4d): the term page at
// /projects/{proj}/term/{slug}, GET /api/v1/projects/{id}/terms, and a
// definition's concept IRI through PATCH /api/v1/rules/{id}.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestTermPageAndList(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: ruleDocV1})
	def, concept := "definition", "https://worklode.io/ns/concept/requirement"
	if rr := doReq(t, h, http.MethodPatch, "/api/v1/rules/WL-REQ-1", token,
		model.RuleMetaInput{Kind: &def, Concept: &concept}); rr.Code != http.StatusOK {
		t.Fatalf("set definition = %d %s", rr.Code, rr.Body)
	}
	if rr := doReq(t, h, http.MethodPost, "/api/v1/rules/WL-REQ-3/edges", token,
		model.RuleEdgeInput{Type: "needs", To: "WL-RULE-1"}); rr.Code >= 300 {
		t.Fatalf("link needs = %d %s", rr.Code, rr.Body)
	}

	rr := doReq(t, h, http.MethodGet, "/projects/"+projID+"/term/one", "", nil)
	body := rr.Body.String()
	for _, want := range []string{"One", "WL-RULE-1", "WL-REQ-3", concept} {
		if rr.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("term page = %d, want %q in\n%s", rr.Code, want, body[:min(1500, len(body))])
		}
	}
	if rr := doReq(t, h, http.MethodGet, "/projects/"+projID+"/term/nope", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown term = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, http.MethodGet, "/WL-RULE-1", "", nil); rr.Code != http.StatusFound ||
		rr.Header().Get("Location") != "/projects/"+projID+"/rule/1" {
		t.Errorf("/WL-RULE-1 = %d %q", rr.Code, rr.Header().Get("Location"))
	}

	rr = doReq(t, h, http.MethodGet, "/api/v1/projects/"+projID+"/terms", token, nil)
	var terms []model.Term
	if rr.Code != http.StatusOK {
		t.Fatalf("list terms = %d %s", rr.Code, rr.Body)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &terms); err != nil {
		t.Fatal(err)
	}
	if len(terms) != 1 || terms[0].Slug != "one" || terms[0].Rule.Ref != "WL-RULE-1" || terms[0].Rule.ConceptIRI != concept {
		t.Errorf("terms = %+v", terms)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/projects/nowhere/terms", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d, want 404", rr.Code)
	}

	bad := "https://worklode.io/ns/concept/nope"
	if rr := doReq(t, h, http.MethodPatch, "/api/v1/rules/WL-RULE-1", token,
		model.RuleMetaInput{Concept: &bad}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad concept = %d, want 422", rr.Code)
	}
}
