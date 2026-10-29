package cmd

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// reviewCmdSpecBody is a spec whose sec-1 rule text holds "rule body" once and
// "body" twice, so one quote is unique and a shorter one is ambiguous.
const reviewCmdSpecBody = "# Reviewed spec\n\nPreamble.\n\n## 1. One {#sec-1}\n\nFirst rule body text.\n\n" +
	"## 2. Two {#sec-2}\n\nSecond body.\n"

// TestReviewDocumentVerbs drives a document review through `lode review`
// (WL-REQ-1775): alice owns the spec, bob reviews it.
func TestReviewDocumentVerbs(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	bobToken, err := st.CreateToken(ctx, "bob", "bob token", nil)
	if err != nil {
		t.Fatalf("create bob token: %v", err)
	}
	aliceToken := os.Getenv("LODE_TOKEN")
	d, _, err := c.CreateDoc(ctx, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 40, Slug: "040-reviewed", Body: reviewCmdSpecBody,
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, err := runLode(t, "doc", "set", "reviewers", "bob", "040-reviewed"); err != nil {
		t.Fatalf("doc set reviewers: %v", err)
	}

	out, err := runLode(t, "review", "add", "040-reviewed", "--json")
	if err != nil {
		t.Fatalf("review add: %v\n%s", err, out)
	}
	var rev model.Review
	if err := json.Unmarshal([]byte(out), &rev); err != nil {
		t.Fatalf("decode review add: %v\n%s", err, out)
	}
	id := strconv.FormatInt(rev.ID, 10)

	t.Setenv("LODE_TOKEN", bobToken)
	_, ambiguous := runLode(t, "review", "comment", id, "--quote", "body", "--intent", "question", "--body", "why?")
	if ambiguous == nil || !strings.Contains(ambiguous.Error(), "occurs 2 times") {
		t.Fatalf("ambiguous quote: err = %v, want it refused", ambiguous)
	}
	detail, _, err := c.GetDoc(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Index(string(detail.Body), "body")
	second := first + 4 + strings.Index(string(detail.Body)[first+4:], "body")
	for _, off := range []int{first, second} {
		if !strings.Contains(ambiguous.Error(), strconv.Itoa(off)) {
			t.Errorf("ambiguous quote error %q does not name offset %d", ambiguous, off)
		}
	}

	out, err = runLode(t, "review", "comment", id, "--quote", "rule body", "--intent", "question",
		"--body", "why this rule?", "--json")
	if err != nil {
		t.Fatalf("review comment: %v\n%s", err, out)
	}
	var th model.ReviewThread
	if err := json.Unmarshal([]byte(out), &th); err != nil {
		t.Fatalf("decode thread: %v\n%s", err, out)
	}
	if th.Anchor.Rule == 0 || th.Anchor.DocAnchor != "sec-1" {
		t.Fatalf("thread anchor = %+v, want the rule at sec-1", th.Anchor)
	}

	t.Setenv("LODE_TOKEN", aliceToken)
	if out, err := runLode(t, "review", "comment", id, "--thread", strconv.FormatInt(th.ID, 10), "--body", "because"); err != nil {
		t.Fatalf("review comment --thread: %v\n%s", err, out)
	}

	t.Setenv("LODE_TOKEN", bobToken)
	if out, err := runLode(t, "review", "approve", id); err != nil {
		t.Fatalf("review approve: %v\n%s", err, out)
	}

	out, err = runLode(t, "review", "show", id, "--json")
	if err != nil {
		t.Fatalf("review show --json: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &rev); err != nil {
		t.Fatalf("decode review show: %v\n%s", err, out)
	}
	if len(rev.Threads) != 1 || len(rev.Threads[0].Messages) != 2 || len(rev.Rounds) != 1 {
		t.Fatalf("review = %+v, want one thread with two messages and one round", rev)
	}
	a := rev.Threads[0].Anchor
	if a.Quote != "rule body" || a.ByteEnd-a.ByteStart != len("rule body") {
		t.Errorf("anchor = %+v, want the byte range of %q", a, "rule body")
	}

	out, err = runLode(t, "review", "show", id)
	if err != nil {
		t.Fatalf("review show: %v\n%s", err, out)
	}
	if !strings.Contains(out, "bob  approved") || !strings.Contains(out, "why this rule?") {
		t.Errorf("review show = %q, want bob's verdict line and the thread", out)
	}

	out, err = runLode(t, "review", "list", "--mine", "--open")
	if err != nil || !strings.Contains(out, "doc "+strconv.FormatInt(d.ID, 10)) {
		t.Errorf("review list --mine = %q, %v; want bob's review", out, err)
	}

	// Nothing after the reply's seq: --timeout elapses, prints nothing.
	out, err = runLode(t, "review", "wait", id, "--after", "2", "--timeout", "1")
	if err != nil || strings.TrimSpace(out) != "" {
		t.Errorf("review wait (nothing new) = %q, %v; want no output", out, err)
	}
	out, err = runLode(t, "review", "wait", id, "--timeout", "1")
	if err != nil {
		t.Fatalf("review wait: %v", err)
	}
	var env struct {
		Review   int64                 `json:"review"`
		After    int64                 `json:"after"`
		Messages []model.ReviewMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || env.Review != rev.ID || env.After != 2 || len(env.Messages) != 2 {
		t.Errorf("review wait envelope = %q (%v), want review %d after 2 with two messages", out, err, rev.ID)
	}
}
