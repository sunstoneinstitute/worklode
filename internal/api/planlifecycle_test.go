package api_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestListDocsTerminalPlans: GET /api/v1/docs shows withdrawn plans unless
// the caller sends hide_terminal=true, which only `lode doc list` does. Every
// other caller, such as the corpus importer's slug lookup, needs the whole
// corpus (final review C1, increment 3).
func TestListDocsTerminalPlans(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25)
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "part-one", Body: docPlanCoveringSec1Body,
	})
	acceptDocViaAPI(t, h, token, plan.ID)
	if rr := doReq(t, h, "POST", docPath(plan.ID, "/withdraw"), token,
		model.WithdrawDocInput{Justification: "dropped"}); rr.Code != http.StatusOK {
		t.Fatalf("withdraw status = %d, body %s", rr.Code, rr.Body.String())
	}
	for query, want := range map[string]int{
		"project=proj&kind=plan":                    1,
		"project=proj&kind=plan&status=all":         1,
		"project=proj&kind=plan&hide_terminal=true": 0,
		"project=proj&kind=plan&status=withdrawn":   1,
	} {
		if got := len(listDocs(t, h, token, query).Docs); got != want {
			t.Errorf("?%s: %d plans, want %d", query, got, want)
		}
	}
	// The cockpit's /docs page is the other user-facing listing: it hides
	// terminal plans unless a status is chosen.
	link := fmt.Sprintf(`href="/projects/proj/plan/%d"`, plan.Number)
	for path, want := range map[string]bool{"/docs": false, "/docs?status=all": true, "/docs?status=withdrawn": true} {
		rr := doReq(t, h, "GET", path, "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rr.Code)
		}
		if got := strings.Contains(rr.Body.String(), link); got != want {
			t.Errorf("%s shows the withdrawn plan = %v, want %v", path, got, want)
		}
	}
}
