package api_test

// reviewrounds_test.go covers the round and events routes of WL-REQ-1323:
// reviewer rounds and their change_request gate (WL-REQ-1777), the round
// read, and the long-poll events feed.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// sampleWith returns the counter value or histogram sample count of the
// series of name whose labels include every pair in want.
func sampleWith(t *testing.T, reg *prometheus.Registry, name string, want map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
	metrics:
		for _, m := range f.GetMetric() {
			got := map[string]string{}
			for _, lp := range m.GetLabel() {
				got[lp.GetName()] = lp.GetValue()
			}
			for k, v := range want {
				if got[k] != v {
					continue metrics
				}
			}
			if m.Histogram != nil {
				return float64(m.GetHistogram().GetSampleCount())
			}
			return m.GetCounter().GetValue()
		}
	}
	t.Fatalf("no series %s%v", name, want)
	return 0
}

// openSpecReview creates spec 31 owned by alice with reviewer a, opens its
// review, and returns the review, a's token and the rendered body's version.
func openSpecReview(t *testing.T, reg *prometheus.Registry) (h http.Handler, token, aToken string, rev model.Review, detail model.DocDetail) {
	t.Helper()
	return openSpecReviewWith(t, api.Config{WebOpen: true, Metrics: reg})
}

// openSpecReviewWith is openSpecReview on a server built from cfg.
func openSpecReviewWith(t *testing.T, cfg api.Config) (h http.Handler, token, aToken string, rev model.Review, detail model.DocDetail) {
	t.Helper()
	st, h, token := newTestServerWithConfig(t, cfg)
	createProject(t, st, "proj")
	aToken = docActor(t, st, "a")
	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 31, Slug: "031-review", Body: reviewSpecBody,
	})
	setReviewers(t, h, token, spec.ID, []string{"a"})
	rr := doReq(t, h, "POST", "/api/v1/reviews", token, model.OpenReviewInput{Doc: spec.ID})
	if rr.Code != http.StatusCreated {
		t.Fatalf("open review status = %d, body %s", rr.Code, rr.Body.String())
	}
	decodeInto(t, rr, &rev)
	decodeInto(t, doReq(t, h, "GET", docPath(spec.ID, ""), token, nil), &detail)
	return h, token, aToken, rev, detail
}

// TestReviewRoundIntentClassify: a crit round's intent-less threads take
// Jev's choices with classifier jev, a stated intent is kept, and an unset key
// or a failing TypeSafe gives change_request with classifier default while the
// round still lands (WL-REQ-1885).
func TestReviewRoundIntentClassify(t *testing.T) {
	t.Parallel()
	var asked atomic.Value
	jev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			State     map[string]any `json:"state"`
			Questions map[string]any `json:"questions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		asked.Store(req.State)
		choices := map[string]string{"t0": "question", "t1": "note"}
		answers := map[string]any{}
		for id := range req.Questions {
			answers[id] = map[string]any{"choice": choices[id], "confidence": 0.8, "probabilities": map[string]float64{choices[id]: 0.8}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer jev.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer broken.Close()

	for _, tc := range []struct {
		name, url, key, intent0, intent1, classifier, outcome string
	}{
		{"jev", jev.URL, "k", "question", "note", "jev", "jev"},
		{"unconfigured", jev.URL, "", "change_request", "change_request", "default", "default_unconfigured"},
		{"failing", broken.URL, "k", "change_request", "change_request", "default", "default_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			h, _, aToken, rev, detail := openSpecReviewWith(t, api.Config{WebOpen: true, Metrics: reg, TypeSafeAPIKey: tc.key, TypeSafeURL: tc.url})
			body := string(detail.Body)
			anchor := func(q string) model.ReviewAnchor {
				i := strings.Index(body, q)
				return model.ReviewAnchor{Doc: rev.Doc, Version: detail.Version, ByteStart: i, ByteEnd: i + len(q), Quote: q}
			}
			roundsPath := fmt.Sprintf("/api/v1/reviews/%d/rounds", rev.ID)
			rr := doReq(t, h, "POST", roundsPath, aToken, model.ReviewRoundInput{
				Verdict: "changes_requested", Client: "crit",
				Threads: []model.OpenThreadInput{
					{Anchor: anchor("First rule"), Body: "why first?"},
					{Anchor: anchor("Second rule"), Body: "nice"},
					{Anchor: anchor("Preamble"), Intent: "question", Body: "stated"},
				},
			})
			if rr.Code != http.StatusCreated {
				t.Fatalf("crit round: %d %s", rr.Code, rr.Body.String())
			}
			var got model.Review
			decodeInto(t, rr, &got)
			byBody := map[string]model.ReviewThread{}
			for _, th := range got.Threads {
				byBody[th.Messages[0].Body] = th
			}
			for b, w := range map[string][3]string{
				"why first?": {tc.intent0, tc.classifier},
				"nice":       {tc.intent1, tc.classifier},
				"stated":     {"question", ""},
			} {
				th := byBody[b]
				if th.Intent != w[0] || th.IntentClassifier != w[1] {
					t.Errorf("thread %q: intent %q classifier %q, want %q %q", b, th.Intent, th.IntentClassifier, w[0], w[1])
				}
				if (th.IntentClassifier == "jev") != (th.IntentConfidence == 0.8) {
					t.Errorf("thread %q: confidence %v with classifier %q", b, th.IntentConfidence, th.IntentClassifier)
				}
			}
			if got := sampleWith(t, reg, "worklode_review_intent_classifications_total",
				map[string]string{"outcome": tc.outcome, "intent": tc.intent0}); got < 1 {
				t.Errorf("intent_classifications_total{%s,%s} = %v", tc.outcome, tc.intent0, got)
			}
			if tc.name == "jev" {
				st, _ := asked.Load().(map[string]any)
				if subj, _ := st["subject"].(map[string]any); subj["kind"] != "doc" || subj["title"] == "" {
					t.Errorf("jev state subject = %v, want the doc's kind and title", st["subject"])
				}
			}
		})
	}
}

func TestReviewRoundApproveGate(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	h, token, aToken, rev, detail := openSpecReview(t, reg)
	rendered := string(detail.Body)
	i := strings.Index(rendered, "rule body")
	anchor := model.ReviewAnchor{Doc: rev.Doc, Version: detail.Version, ByteStart: i, ByteEnd: i + 9, Quote: "rule body"}
	roundsPath := fmt.Sprintf("/api/v1/reviews/%d/rounds", rev.ID)

	rr := doReq(t, h, "POST", roundsPath, aToken, model.ReviewRoundInput{
		Verdict: "approved",
		Threads: []model.OpenThreadInput{{Anchor: anchor, Intent: "change_request", Body: "fix this"}},
	})
	if rr.Code != http.StatusUnprocessableEntity || !regexp.MustCompile(`change_request threads \d+ are open`).MatchString(rr.Body.String()) {
		t.Fatalf("approve over a change_request: status = %d, body %s; want 422 naming the thread", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", roundsPath, aToken, model.ReviewRoundInput{
		Verdict: "approved",
		Threads: []model.OpenThreadInput{{Anchor: anchor, Intent: "note", Body: "fine"}},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("approve with a note: status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "GET", roundsPath+"/1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get round 1: status = %d, body %s", rr.Code, rr.Body.String())
	}
	var rd model.ReviewRound
	decodeInto(t, rr, &rd)
	if rd.Client != "cli" || rd.Focused || rd.Verdict != "approved" || rd.Actor != "a" {
		t.Errorf("round 1 = %+v, want a's approval with client cli, not focused", rd)
	}
	if rr := doReq(t, h, "GET", roundsPath+"/2", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("get round 2: status = %d, want 404", rr.Code)
	}

	if got := sampleWith(t, reg, "worklode_review_rounds_total", map[string]string{"subject": "doc", "client": "cli"}); got != 1 {
		t.Errorf("rounds_total{doc,cli} = %v, want 1", got)
	}
	if got := sampleWith(t, reg, "worklode_review_verdicts_total", map[string]string{"verdict": "approved", "reviewer_kind": "human"}); got != 1 {
		t.Errorf("verdicts_total{approved,human} = %v, want 1", got)
	}
	if got := sampleWith(t, reg, "worklode_review_time_to_verdict_seconds", nil); got != 1 {
		t.Errorf("time_to_verdict observations = %v, want 1", got)
	}

	for name, body := range map[string]model.ReviewRoundInput{
		"neither":     {},
		"both":        {Verdict: "approved", BaseRef: "2"},
		"bad client":  {Verdict: "approved", Client: "email"},
		"bad verdict": {Verdict: "maybe"},
	} {
		if rr := doReq(t, h, "POST", roundsPath, aToken, body); rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422; body %s", name, rr.Code, rr.Body.String())
		}
	}
}

func TestReviewEventsLongPoll(t *testing.T) {
	t.Parallel()
	h, token, aToken, rev, detail := openSpecReview(t, nil)
	rendered := string(detail.Body)
	i := strings.Index(rendered, "rule body")
	revPath := fmt.Sprintf("/api/v1/reviews/%d", rev.ID)
	rr := doReq(t, h, "POST", revPath+"/threads", aToken, model.OpenThreadInput{
		Anchor: model.ReviewAnchor{Doc: rev.Doc, Version: detail.Version, ByteStart: i, ByteEnd: i + 9, Quote: "rule body"},
		Intent: "question", Body: "why?",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("open thread status = %d, body %s", rr.Code, rr.Body.String())
	}
	var th model.ReviewThread
	decodeInto(t, rr, &th)
	seq := th.Messages[0].Seq

	// Nothing after the latest message: 204 at the deadline.
	if rr := doReq(t, h, "GET", fmt.Sprintf("%s/events?after=%d&timeout=0", revPath, seq), token, nil); rr.Code != http.StatusNoContent {
		t.Fatalf("idle poll status = %d, want 204; body %s", rr.Code, rr.Body.String())
	}

	type result struct {
		code int
		ev   model.ReviewEvents
		took time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		rr := doReq(t, h, "GET", fmt.Sprintf("%s/events?after=%d&timeout=5", revPath, seq), token, nil)
		var res result
		res.code, res.took = rr.Code, time.Since(start)
		if rr.Code == http.StatusOK {
			decodeInto(t, rr, &res.ev)
		}
		done <- res
	}()
	time.Sleep(200 * time.Millisecond)
	if rr := doReq(t, h, "POST", fmt.Sprintf("%s/threads/%d/messages", revPath, th.ID), token,
		model.ThreadReplyInput{Body: "because"}); rr.Code != http.StatusCreated {
		t.Fatalf("reply status = %d, body %s", rr.Code, rr.Body.String())
	}
	res := <-done
	if res.code != http.StatusOK || res.took > 2*time.Second {
		t.Fatalf("poll = %d after %v, want 200 within 2s", res.code, res.took)
	}
	if len(res.ev.Messages) != 1 || res.ev.Messages[0].Body != "because" || res.ev.After != int64(res.ev.Messages[0].Seq) {
		t.Errorf("events = %+v, want the reply with after at its seq", res.ev)
	}
	if len(res.ev.Threads) != 1 || res.ev.Threads[0].ID != th.ID {
		t.Errorf("events threads = %+v, want thread %d", res.ev.Threads, th.ID)
	}
}

// TestChangeReviewRounds: a change round moves the change metric, the lease
// holder's verdict is 403, and changes_requested returns the task to
// in_progress (WL-REQ-1324, WL-RULE-1321).
func TestChangeReviewRounds(t *testing.T) {
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
	rounds := fmt.Sprintf("/api/v1/reviews/%d/rounds", rev.ID)

	if rr := doReq(t, h, "POST", rounds, token, model.ReviewRoundInput{Verdict: "approved"}); rr.Code != http.StatusForbidden {
		t.Errorf("lease holder's verdict = %d, want 403, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, h, "POST", rounds, aToken, model.ReviewRoundInput{Verdict: "changes_requested"}); rr.Code != http.StatusCreated {
		t.Fatalf("round = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := sampleWith(t, reg, "worklode_review_rounds_total", map[string]string{"subject": "change", "client": "cli"}); got != 1 {
		t.Errorf("rounds metric = %v, want 1", got)
	}
	var task model.Task
	decodeInto(t, doReq(t, h, "GET", "/api/v1/tasks/"+id, token, nil), &task)
	if task.State != "in_progress" {
		t.Errorf("task state = %s, want in_progress", task.State)
	}
}
