package api_test

import (
	"fmt"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestRuleShowCarriesInlineRefs: GET /api/v1/rules/{ref} lists the inline
// references the rule makes to a task, a doc and a milestone, and the one a
// task makes to the rule (WL-REQ-1852).
func TestRuleShowCarriesInlineRefs(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Target", "priority": "medium", "kind": "feature"})
	doc := createDocViaAPI(t, h, token, model.CreateDocInput{Project: "proj", Kind: "plan", Slug: "named", Body: model.Markdown("# Named\n")})
	rr := doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token, model.CreateMilestoneInput{Title: "Named"})
	mile := decodeMap(t, rr)["id"].(string)

	docRef := fmt.Sprintf("WL-PLAN-%d", doc.Number)
	rr = doReq(t, h, "POST", "/api/v1/rules", token, model.AddRuleInput{Project: "proj", Heading: "Names three",
		Body: model.Markdown(fmt.Sprintf("See WL-1, %s and %s.\n", docRef, mile))})
	if rr.Code != 201 {
		t.Fatalf("add rule: %d %s", rr.Code, rr.Body.String())
	}
	ruleRef := decodeMap(t, rr)["ref"].(string)
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Source", "priority": "medium", "kind": "feature", "body": "Governed by " + ruleRef + ".\n"})

	var d model.RuleDetail
	decodeInto(t, doReq(t, h, "GET", "/api/v1/rules/"+ruleRef, token, nil), &d)
	got := map[string]bool{}
	for _, r := range d.Out {
		got[string(r.ToKind)+" "+r.ToRef] = true
	}
	for _, want := range []string{"task WL-1", "doc " + docRef, "milestone " + mile} {
		if !got[want] {
			t.Errorf("inline_refs_out lacks %q: %v", want, d.Out)
		}
	}
	if len(d.In) != 1 || d.In[0].FromRef != "WL-2" {
		t.Errorf("inline_refs_in = %v, want WL-2", d.In)
	}
}
