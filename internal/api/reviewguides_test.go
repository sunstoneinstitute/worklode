package api_test

import (
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestReviewGuideRoutes: the lease holder proposes the TestComposeHonesty
// guide, the refusal and skims are counted, reads honour focused, a reviewer
// cannot propose, and a review with no guide is 404 (WL-REQ-1323, WL-REQ-1327).
func TestReviewGuideRoutes(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	st, h, token := newTestServerWithConfig(t, api.Config{WebOpen: true, Metrics: reg})
	createProject(t, st, "proj")
	aToken := docActor(t, st, "a")
	id := createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "t", "priority": "low", "kind": "feature"})["id"].(string)
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/reviewers", token,
		model.SetTaskReviewersInput{Reviewers: []string{"a", "alice"}}); rr.Code != http.StatusOK {
		t.Fatalf("set reviewers = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/claim", token, map[string]any{"worktree": "host:/wt"}); rr.Code != http.StatusOK {
		t.Fatalf("claim = %d, body %s", rr.Code, rr.Body.String())
	}
	rr := doReq(t, h, "POST", "/api/v1/reviews", token,
		model.OpenReviewInput{Task: id, BaseRef: "0123456789abcdef0123456789abcdef01234567"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("open = %d, body %s", rr.Code, rr.Body.String())
	}
	var rev model.Review
	decodeInto(t, rr, &rev)
	guide := fmt.Sprintf("/api/v1/reviews/%d/guide", rev.ID)

	if rr := doReq(t, h, "GET", guide, token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("GET with no guide = %d, want 404", rr.Code)
	}
	diff, err := os.ReadFile("testdata/guide-mixed.diff")
	if err != nil {
		t.Fatal(err)
	}
	span := func(from, to int) []model.GuideSpan { return []model.GuideSpan{{From: from, To: to}} }
	prop := model.GuideProposal{
		Overview: "o",
		Files: []model.GuideFile{
			{Path: "go.sum", Skims: span(1, 40)},
			{Path: "internal/x/x.go", Skims: span(10, 20)},
			{Path: "internal/y/y_templ.go"},
		},
		Diff: string(diff),
	}
	if rr := doReq(t, h, "PUT", guide, aToken, prop); rr.Code != http.StatusForbidden {
		t.Fatalf("PUT as reviewer = %d, want 403, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "PUT", guide, token, prop)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d, body %s", rr.Code, rr.Body.String())
	}
	var res model.ReviewGuideResult
	decodeInto(t, rr, &res)
	if len(res.Refused) != 1 || res.Refused[0].Rule != "non-mechanical" {
		t.Errorf("refused = %+v, want one non-mechanical", res.Refused)
	}
	if got := sampleWith(t, reg, "worklode_review_guide_skims_refused_total", map[string]string{"rule": "non-mechanical"}); got != 1 {
		t.Errorf("refused metric = %v, want 1", got)
	}
	if got := sampleWith(t, reg, "worklode_review_skimmed_blocks_total", map[string]string{"decided_by": "policy"}); got != 1 {
		t.Errorf("policy skim metric = %v, want 1", got)
	}

	skims := func(path string) int {
		var g model.ReviewGuide
		decodeInto(t, doReq(t, h, "GET", path, token, nil), &g)
		n := 0
		for _, f := range g.Files {
			n += len(f.Skims)
		}
		return n
	}
	if n := skims(guide); n != 0 {
		t.Errorf("GET without focused has %d skims, want 0", n)
	}
	if n := skims(guide + "?focused=true"); n != 2 {
		t.Errorf("GET focused=true has %d skims, want 2", n)
	}

	if rr := doReq(t, h, "POST", fmt.Sprintf("/api/v1/reviews/%d/rounds", rev.ID), aToken,
		model.ReviewRoundInput{Verdict: "approved", Focused: true}); rr.Code != http.StatusCreated {
		t.Fatalf("focused round = %d, body %s", rr.Code, rr.Body.String())
	}
	var rd model.ReviewRound
	decodeInto(t, doReq(t, h, "GET", fmt.Sprintf("/api/v1/reviews/%d/rounds/1", rev.ID), token, nil), &rd)
	if !rd.Focused {
		t.Errorf("round focused = false, want stored true")
	}
}
