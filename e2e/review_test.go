//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// reviewSpecBody is the spec under review. The change request quotes text
// inside sec-1's rule; the question quotes template text.
const reviewSpecBody = `---
status: draft
---

# Reviewed Spec

Opening words.

## 1. Quoting {#sec-1}

The server must keep the quoted words in place.

## 2. Model {#sec-2}

Model body.
`

const (
	crQuote       = "keep the quoted words in place"
	questionQuote = "Opening words."
)

// TestDocumentReview drives one document review from open to accepted over
// the HTTP API (WL-RULE-1315): threads anchored on the rendered spec, the
// change_request gate on approval, a rewrite that moves the quoted text, the
// author round, both reviewer lanes, accept closing the review, the review
// events in the log and the WL-REQ-1327 metric families on /metrics.
func TestDocumentReview(t *testing.T) {
	ctx := context.Background()

	reg := prometheus.NewRegistry()
	st := store.OpenTestStore(t, store.WithMetrics(reg))
	main, adminHandler, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		Metrics:        reg,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(main)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "review", Name: "Review", Key: "RV",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	clients := map[string]*cli.Client{}
	for _, id := range []string{"owner", "a", "b"} {
		if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{ID: id, Kind: "human", DisplayName: id}); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
		tok, _, err := admin.CreateToken(ctx, id, "e2e document review", nil)
		if err != nil {
			t.Fatalf("create token for %s: %v", id, err)
		}
		clients[id] = cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})
	}
	owner, a, b := clients["owner"], clients["a"], clients["b"]

	// 1. The owner writes the spec, names the reviewers, submits and opens
	// the review.
	doc, _, err := owner.CreateDoc(ctx, model.CreateDocInput{
		Project: "review", Kind: "spec", Number: 1, Slug: "reviewed-spec",
		Body: reviewSpecBody, Owner: "owner",
	})
	if err != nil {
		t.Fatalf("create spec: %v", err)
	}
	if _, _, err := owner.SetDocReviewers(ctx, doc.ID, []string{"a", "b"}); err != nil {
		t.Fatalf("set reviewers: %v", err)
	}
	if _, _, err := owner.SubmitDoc(ctx, doc.ID); err != nil {
		t.Fatalf("submit doc: %v", err)
	}
	rev, _, err := owner.OpenReview(ctx, model.OpenReviewInput{Doc: doc.ID})
	if err != nil {
		t.Fatalf("open review: %v", err)
	}
	if rev.State != "open" || len(rev.Reviewers) != 2 {
		t.Fatalf("opened review = state %q reviewers %v, want open with a and b", rev.State, rev.Reviewers)
	}

	// 2. a opens a change_request inside sec-1's rule and a question on
	// template text, both as ranges of the rendered spec (WL-REQ-1317).
	rendered, _, err := owner.GetDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	openOn := func(quote, intent, body string) model.ReviewThread {
		t.Helper()
		start, err := cli.FindQuote(string(rendered.Body), quote, "the rendered spec")
		if err != nil {
			t.Fatal(err)
		}
		th, _, err := a.OpenReviewThread(ctx, rev.ID, model.OpenThreadInput{
			Anchor: model.ReviewAnchor{Doc: doc.ID, Version: rendered.Version,
				ByteStart: start, ByteEnd: start + len(quote), Quote: quote},
			Intent: intent, Body: body,
		})
		if err != nil {
			t.Fatalf("open %s on %q: %v", intent, quote, err)
		}
		return th
	}
	cr := openOn(crQuote, "change_request", "Say where the words go.")
	question := openOn(questionQuote, "question", "Who reads this first?")

	// The change request reads back as a rule thread: the bytes are the
	// rule's (WL-REQ-1317).
	if cr.Anchor.Rule == 0 || cr.Anchor.Doc != 0 || cr.Anchor.Quote != crQuote || cr.Anchor.DocAnchor != "sec-1" {
		t.Fatalf("change request anchor = %+v, want a rule anchor at sec-1 quoting %q", cr.Anchor, crQuote)
	}
	rules, _, err := owner.ListRules(ctx, model.RuleListParams{Doc: strconv.FormatInt(doc.ID, 10)})
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	var rule model.Rule
	for _, r := range rules {
		if r.ID == cr.Anchor.Rule {
			rule = r
		}
	}
	if rule.ID == 0 {
		t.Fatalf("rule %d of the change request is not arranged in doc %d: %+v", cr.Anchor.Rule, doc.ID, rules)
	}
	if rule, _, err = owner.GetRule(ctx, rule.Ref); err != nil {
		t.Fatalf("get rule %s: %v", rule.Ref, err)
	}
	if got := strings.Index(string(rule.Body), crQuote); cr.Anchor.ByteStart != got || cr.Anchor.Version != rule.Version {
		t.Fatalf("change request = v%d at byte %d, want v%d at %d in rule %s", cr.Anchor.Version, cr.Anchor.ByteStart,
			rule.Version, got, rule.Ref)
	}

	// 3. The owner answers the question; a's approval is refused while the
	// change request is open (WL-REQ-1777).
	if _, _, err := owner.ReplyReviewThread(ctx, rev.ID, question.ID, "Reviewers do."); err != nil {
		t.Fatalf("answer question: %v", err)
	}
	_, _, err = a.SubmitReviewRound(ctx, rev.ID, model.ReviewRoundInput{Verdict: "approved", Client: "cli"})
	if err == nil || clientErrStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("approve with an open change request: err = %v, want 422", err)
	}
	// The author round is refused while a question is open (WL-REQ-1774),
	// so a closes the answered question.
	if _, _, err := a.ResolveReviewThread(ctx, rev.ID, question.ID, ""); err != nil {
		t.Fatalf("resolve question: %v", err)
	}

	// 4. The owner rewrites the spec through `lode doc edit`'s write: a
	// paragraph lands before the quoted words inside the rule.
	edited := strings.Replace(string(rendered.Body), "The server must keep",
		"Quotes are anchors.\n\nThe server must keep", 1)
	updated, _, err := owner.UpdateDocBody(ctx, doc.ID, model.UpdateDocBodyInput{Body: model.Markdown(edited)})
	if err != nil {
		t.Fatalf("edit spec: %v", err)
	}
	if updated.Version <= rendered.Version {
		t.Fatalf("doc version after edit = %d, want past %d", updated.Version, rendered.Version)
	}
	rule2, _, err := owner.GetRule(ctx, rule.Ref)
	if err != nil {
		t.Fatalf("get rule %s: %v", rule.Ref, err)
	}
	threads, _, err := owner.DocThreads(ctx, doc.ID, model.DocThreadsParams{})
	if err != nil {
		t.Fatalf("doc threads: %v", err)
	}
	var moved *model.ReviewThread
	for i := range threads {
		if threads[i].ID == cr.ID {
			moved = &threads[i]
		}
	}
	want := strings.Index(string(rule2.Body), crQuote)
	if moved == nil || moved.Status != "open" || moved.Anchor.Version != rule2.Version ||
		moved.Anchor.ByteStart != want || moved.Anchor.ByteEnd != want+len(crQuote) || moved.Anchor.Quote != crQuote {
		t.Fatalf("change request after edit = %+v, want open on rule %s v%d at [%d,%d)", moved, rule.Ref,
			rule2.Version, want, want+len(crQuote))
	}
	if moved.Anchor.ByteStart == cr.Anchor.ByteStart {
		t.Fatalf("change request stayed at byte %d; the edit moved the quoted text", cr.Anchor.ByteStart)
	}

	// 5. The author round at the new version, then a resolves the change
	// request and both reviewers approve.
	if _, _, err := owner.SubmitReviewRound(ctx, rev.ID, model.ReviewRoundInput{
		BaseRef: strconv.Itoa(updated.Version), Client: "cli",
	}); err != nil {
		t.Fatalf("author round: %v", err)
	}
	if _, _, err := a.ResolveReviewThread(ctx, rev.ID, cr.ID, ""); err != nil {
		t.Fatalf("resolve change request: %v", err)
	}
	for name, c := range map[string]*cli.Client{"a": a, "b": b} {
		if _, _, err := c.SubmitReviewRound(ctx, rev.ID, model.ReviewRoundInput{Verdict: "approved", Client: "cli"}); err != nil {
			t.Fatalf("%s approves: %v", name, err)
		}
	}

	// 6. Accept closes the review.
	if _, _, err := owner.AcceptDoc(ctx, doc.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	closed, _, err := owner.GetReview(ctx, rev.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if closed.State != "closed" || closed.ClosedAt == nil {
		t.Fatalf("review after accept = state %q closed_at %v, want closed", closed.State, closed.ClosedAt)
	}

	// 7. The event log (WL-REQ-1780).
	count := func(typ string) []model.Event {
		t.Helper()
		page, _, err := admin.ListEvents(ctx, model.EventListParams{Type: typ})
		if err != nil {
			t.Fatalf("list %s events: %v", typ, err)
		}
		return page.Events
	}
	for typ, n := range map[string]int{
		eventbus.TypeReviewOpened:           1,
		eventbus.TypeReviewQuestionAsked:    1,
		eventbus.TypeReviewQuestionAnswered: 1,
		eventbus.TypeReviewVerdictRecorded:  2,
		eventbus.TypeReviewClosed:           1,
	} {
		if got := len(count(typ)); got != n {
			t.Errorf("%s events = %d, want %d", typ, got, n)
		}
	}
	resolved := 0
	for _, ev := range count(eventbus.TypeReviewThreadResolved) {
		if eventPayload(t, eventByID(t, ctx, admin, ev.ID))["wl:threadStatus"] == "resolved" {
			resolved++
		}
	}
	if resolved < 2 {
		t.Errorf("%s events with status resolved = %d, want the question and the change request", eventbus.TypeReviewThreadResolved, resolved)
	}

	// 8. The WL-REQ-1327 families on /metrics.
	rec := httptest.NewRecorder()
	adminHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics: status = %d", rec.Code)
	}
	for _, family := range []string{
		"worklode_review_rounds_total", "worklode_review_threads_total", "worklode_review_open_threads",
		"worklode_review_question_latency_seconds", "worklode_review_time_to_verdict_seconds",
		"worklode_review_verdicts_total",
	} {
		if !strings.Contains(rec.Body.String(), family) {
			t.Errorf("/metrics missing %s", family)
		}
	}
}
