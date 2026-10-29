package api_test

// reviews_test.go covers the review routes of WL-REQ-1323 this part serves:
// open and read a review, open, reply to and resolve threads, bare notes and
// the two thread lists, and the worklode_review_* metrics they move
// (WL-REQ-1327).

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// reviewSpecBody has template text between rule 1's text and rule 2.
const reviewSpecBody = "---\nstatus: draft\n---\n# T\n\nPreamble text.\n\n## 1. One {#sec-1}\n\nFirst rule body text.\n\n" +
	"#### Example\n\nTemplate between.\n\n## 2. Two {#sec-2}\n\nSecond rule body.\n"

// sampleOf returns the value of a counter or gauge, or the sample count of a
// histogram, for the series of name carrying label=value ("" for none).
func sampleOf(t *testing.T, reg *prometheus.Registry, name, label, value string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			match := label == ""
			for _, lp := range m.GetLabel() {
				if lp.GetName() == label && lp.GetValue() == value {
					match = true
				}
			}
			if !match {
				continue
			}
			switch {
			case m.Counter != nil:
				return m.GetCounter().GetValue()
			case m.Gauge != nil:
				return m.GetGauge().GetValue()
			case m.Histogram != nil:
				return float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	t.Fatalf("no series %s{%s=%q}", name, label, value)
	return 0
}

func TestReviewThreadLifecycle(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	st, h, token := newTestServerWithConfig(t, api.Config{WebOpen: true, Metrics: reg}) // alice owns the spec
	createProject(t, st, "proj")
	aToken := docActor(t, st, "a")
	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 31, Slug: "031-review", Body: reviewSpecBody,
	})
	setReviewers(t, h, token, spec.ID, []string{"a"})

	rr := doReq(t, h, "POST", "/api/v1/reviews", token, model.OpenReviewInput{Doc: spec.ID})
	if rr.Code != http.StatusCreated {
		t.Fatalf("open review status = %d, body %s", rr.Code, rr.Body.String())
	}
	var rev model.Review
	decodeInto(t, rr, &rev)
	if rev.State != "open" || fmt.Sprint(rev.Reviewers) != "[a]" {
		t.Fatalf("review = %+v, want open with roster [a]", rev)
	}
	if rr := doReq(t, h, "POST", "/api/v1/reviews", token, model.OpenReviewInput{Doc: spec.ID}); rr.Code != http.StatusConflict {
		t.Errorf("second open status = %d, want 409", rr.Code)
	}

	rr = doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	rendered := string(detail.Body)
	revPath := fmt.Sprintf("/api/v1/reviews/%d", rev.ID)

	// A question on bytes of rule 1's text, as reviewer a.
	i := strings.Index(rendered, "rule body")
	rr = doReq(t, h, "POST", revPath+"/threads", aToken, model.OpenThreadInput{
		Anchor: model.ReviewAnchor{Doc: spec.ID, Version: detail.Version, ByteStart: i, ByteEnd: i + 9, Quote: "rule body"},
		Intent: "question", Body: "why this rule?",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("open thread status = %d, body %s", rr.Code, rr.Body.String())
	}
	var q model.ReviewThread
	decodeInto(t, rr, &q)
	if q.Anchor.Rule == 0 || q.Anchor.Doc != 0 || q.Anchor.DocAnchor != "sec-1" || q.Author != "a" {
		t.Fatalf("thread anchor = %+v author %q, want rule 1 at sec-1 by a", q.Anchor, q.Author)
	}

	// The owner answers; a resolves.
	tPath := fmt.Sprintf("%s/threads/%d", revPath, q.ID)
	rr = doReq(t, h, "POST", tPath+"/messages", token, model.ThreadReplyInput{Body: "because"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("reply status = %d, body %s", rr.Code, rr.Body.String())
	}
	var m model.ReviewMessage
	decodeInto(t, rr, &m)
	if m.Role != "author" {
		t.Errorf("owner reply role = %q, want author", m.Role)
	}
	rr = doReq(t, h, "POST", tPath+"/resolve", aToken, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, body %s", rr.Code, rr.Body.String())
	}
	var resolved model.ReviewThread
	decodeInto(t, rr, &resolved)
	if resolved.Status != "resolved" || resolved.ResolvedBy != "a" {
		t.Errorf("resolved = %+v, want resolved by a", resolved)
	}

	rr = doReq(t, h, "GET", revPath, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get review status = %d", rr.Code)
	}
	decodeInto(t, rr, &rev)
	if len(rev.Threads) != 1 || rev.Threads[0].Status != "resolved" || len(rev.Threads[0].Messages) != 2 {
		t.Errorf("review threads = %+v, want one resolved thread with two messages", rev.Threads)
	}

	if got := sampleOf(t, reg, "worklode_review_threads_total", "intent", "question"); got != 1 {
		t.Errorf("threads_total{intent=question} = %v, want 1", got)
	}
	if got := sampleOf(t, reg, "worklode_review_question_latency_seconds", "", ""); got != 1 {
		t.Errorf("question_latency observations = %v, want 1", got)
	}

	// A bare note on sec-1 reads back beside the review thread.
	rr = doReq(t, h, "POST", docPath(spec.ID, "/threads"), aToken, model.OpenThreadInput{
		Anchor: model.ReviewAnchor{DocAnchor: "sec-1"}, Intent: "note", Body: "a bare note",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("bare note status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := sampleOf(t, reg, "worklode_review_open_threads", "subject", "doc"); got != 1 {
		t.Errorf("open_threads{subject=doc} = %v, want 1 (the bare note)", got)
	}
	var threads []model.ReviewThread
	rr = doReq(t, h, "GET", docPath(spec.ID, "/threads?anchor=sec-1"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("doc threads status = %d, body %s", rr.Code, rr.Body.String())
	}
	decodeInto(t, rr, &threads)
	if len(threads) != 2 || threads[0].ID != q.ID || threads[1].Review != 0 {
		t.Errorf("doc threads = %+v, want the question then the bare note", threads)
	}

	// The rule's list holds the question.
	var rules []model.Rule
	decodeInto(t, doReq(t, h, "GET", "/api/v1/rules?project=proj", token, nil), &rules)
	ref := ""
	for _, r := range rules {
		if r.ID == q.Anchor.Rule {
			ref = r.Ref
		}
	}
	rr = doReq(t, h, "GET", "/api/v1/rules/"+ref+"/threads", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("rule threads status = %d, body %s", rr.Code, rr.Body.String())
	}
	decodeInto(t, rr, &threads)
	if len(threads) != 1 || threads[0].ID != q.ID {
		t.Errorf("rule %s threads = %+v, want the question", ref, threads)
	}

	// A range from rule 1's text into the template is refused.
	end := strings.Index(rendered, "text.\n") + len("text.")
	ti := strings.Index(rendered, "Template between")
	rr = doReq(t, h, "POST", revPath+"/threads", aToken, model.OpenThreadInput{
		Anchor: model.ReviewAnchor{Doc: spec.ID, Version: detail.Version, ByteStart: end - 4, ByteEnd: ti + 3, Quote: rendered[end-4 : ti+3]},
		Intent: "note", Body: "crosses",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("crossing range status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
}

// TestReviewThreadScopedRoutes: the thread-scoped reply and resolve serve a
// bare note, and the review-scoped spelling refuses a thread of another
// review.
func TestReviewThreadScopedRoutes(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 32, Slug: "032-notes", Body: reviewSpecBody,
	})
	rr := doReq(t, h, "POST", docPath(spec.ID, "/threads"), token, model.OpenThreadInput{
		Anchor: model.ReviewAnchor{DocAnchor: "sec-2"}, Intent: "note", Body: "note",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("bare note status = %d, body %s", rr.Code, rr.Body.String())
	}
	var note model.ReviewThread
	decodeInto(t, rr, &note)
	if rr := doReq(t, h, "POST", fmt.Sprintf("/api/v1/reviews/999/threads/%d/messages", note.ID), token,
		model.ThreadReplyInput{Body: "x"}); rr.Code != http.StatusNotFound {
		t.Errorf("reply through a review the thread is not in: status = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, "POST", fmt.Sprintf("/api/v1/threads/%d/messages", note.ID), token,
		model.ThreadReplyInput{Body: "reply"}); rr.Code != http.StatusCreated {
		t.Errorf("reply status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", fmt.Sprintf("/api/v1/threads/%d/resolve", note.ID), token, model.ThreadResolveInput{Status: "obsolete"})
	if rr.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got []model.ReviewThread
	decodeInto(t, doReq(t, h, "GET", docPath(spec.ID, "/threads?status=obsolete"), token, nil), &got)
	if len(got) != 1 || len(got[0].Messages) != 2 {
		t.Errorf("obsolete threads = %+v, want the note with two messages", got)
	}
}
