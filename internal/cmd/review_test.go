package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/gitexec"
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

// changeRepo builds a repo on a feature branch whose file sits three lines
// deep, and returns its dir.
func changeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if err := gitexec.Run(dir, append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	run("checkout", "-q", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "f.go"), []byte("package f\n\nfunc a() {}\n\nfunc b() { work() }\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "feature")
	return dir
}

// TestReviewChangeSubmitReanchors opens a change review from a checkout,
// comments on a line, rewrites the file above it and submits: the thread
// follows its line (WL-REQ-1317, WL-REQ-1775).
func TestReviewChangeSubmitReanchors(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
		t.Fatal(err)
	}
	task := createTestTask(t, c, "Change under review")
	dir := changeRepo(t)
	t.Chdir(dir)
	if _, _, err := c.ClaimTask(ctx, task.ID, dir, time.Hour); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, err := c.SetTaskReviewers(ctx, task.ID, []string{"bob"}); err != nil {
		t.Fatalf("set reviewers: %v", err)
	}

	if _, err := runLode(t, "review", "add", task.ID); err == nil || !strings.Contains(err.Error(), "--reason") {
		t.Fatalf("lease holder without --reason: err = %v, want it refused", err)
	}
	out, err := runLode(t, "review", "add", task.ID, "--reason", "touches the route table", "--json")
	if err != nil {
		t.Fatalf("review add: %v\n%s", err, out)
	}
	var rev model.Review
	if err := json.Unmarshal([]byte(out), &rev); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	id := strconv.FormatInt(rev.ID, 10)

	out, err = runLode(t, "review", "comment", id, "--anchor", "worklode:f.go:additions:5",
		"--intent", "change_request", "--body", "check the error", "--json")
	if err != nil {
		t.Fatalf("review comment: %v\n%s", err, out)
	}
	var th model.ReviewThread
	if err := json.Unmarshal([]byte(out), &th); err != nil || th.Anchor.Line != 5 || th.Anchor.AnchorText != "func b() { work() }" {
		t.Fatalf("thread = %+v (%v), want line 5 with the line's text", th.Anchor, err)
	}

	os.WriteFile(filepath.Join(dir, "f.go"), []byte("package f\n\n// new\n// lines\n\nfunc a() {}\n\nfunc b() { work() }\n"), 0o644)
	if err := gitexec.Run(dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-am", "move"); err != nil {
		t.Fatal(err)
	}
	out, err = runLode(t, "review", "submit", id)
	if err != nil {
		t.Fatalf("review submit: %v\n%s", err, out)
	}
	t.Logf("submit output:\n%s", out)
	if want := "thread " + strconv.FormatInt(th.ID, 10) + "  f.go:5 -> 8  "; !strings.Contains(out, want) {
		t.Errorf("submit output = %q, want a stage line %q", out, want)
	}

	out, err = runLode(t, "review", "show", id, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &rev); err != nil || len(rev.Threads) != 1 || rev.Threads[0].Anchor.Line != 8 {
		t.Fatalf("review after submit = %+v (%v), want the thread at line 8", rev, err)
	}
	out, err = runLode(t, "review", "show", id)
	if err != nil || !strings.Contains(out, "diff --git") || !strings.Contains(out, "+func b() { work() }") {
		t.Errorf("review show = %q (%v), want the raw diff after the threads", out, err)
	}
	out, err = runLode(t, "task", "show", task.ID)
	if err != nil || !strings.Contains(out, "opened by alice: touches the route table") {
		t.Errorf("task show = %q (%v), want the review line", out, err)
	}
}

// TestReviewGuide proposes a guide from a worktree: the skim on an edited file
// is refused, the generated file gets a policy skim, an author round makes the
// guide stale, and materializeGuide splits skims by focused (WL-REQ-1775,
// WL-REQ-1326, WL-RULE-1320).
func TestReviewGuide(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
		t.Fatal(err)
	}
	task := createTestTask(t, c, "Change with a guide")
	dir := changeRepo(t)
	git := func(args ...string) {
		t.Helper()
		if err := gitexec.Run(dir, append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "gen.go"), []byte("// Code generated by tool. DO NOT EDIT.\n\npackage f\n\nvar x = 1\n"), 0o644)
	git("add", "-A")
	git("commit", "-q", "-m", "generated")
	t.Chdir(dir)
	if _, _, err := c.ClaimTask(ctx, task.ID, dir, time.Hour); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, err := c.SetTaskReviewers(ctx, task.ID, []string{"bob"}); err != nil {
		t.Fatalf("set reviewers: %v", err)
	}
	out, err := runLode(t, "review", "add", task.ID, "--reason", "needs a look", "--json")
	if err != nil {
		t.Fatalf("review add: %v\n%s", err, out)
	}
	var rev model.Review
	if err := json.Unmarshal([]byte(out), &rev); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(rev.ID, 10)

	if out, err = runLode(t, "review", "show", id); err != nil || !strings.Contains(out, "no guide") {
		t.Errorf("show before a guide = %q (%v), want \"no guide\"", out, err)
	}
	gf := filepath.Join(t.TempDir(), "guide.json")
	os.WriteFile(gf, []byte(`[{"path":"f.go","orientation":"the logic","scrutiny":true,"skims":[{"from":1,"to":5,"decided_by":"author"}]}]`), 0o644)
	out, err = runLode(t, "review", "set", "guide", id, "--file", gf, "--overview", "small change")
	if err != nil || !strings.Contains(out, "refused skim on f.go: non-mechanical") {
		t.Fatalf("set guide = %q (%v), want the refusal named", out, err)
	}

	out, err = runLode(t, "review", "guide", id, "--focused", "--json")
	if err != nil {
		t.Fatalf("review guide: %v\n%s", err, out)
	}
	var g model.ReviewGuide
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatal(err)
	}
	skims := map[string]int{}
	for _, f := range g.Files {
		for _, sk := range f.Skims {
			if sk.DecidedBy != "policy" {
				t.Errorf("skim on %s decided by %q, want policy", f.Path, sk.DecidedBy)
			}
			skims[f.Path]++
		}
	}
	if skims["gen.go"] != 1 || len(skims) != 1 {
		t.Errorf("focused skims = %v, want one policy skim on gen.go only", skims)
	}

	out, err = runLode(t, "review", "show", id, "--json")
	if err != nil || json.Unmarshal([]byte(out), &rev) != nil || rev.Guide == nil || rev.Guide.Stale {
		t.Fatalf("show --json = %q (%v), want a fresh guide embedded", out, err)
	}
	for _, focused := range []bool{true, false} {
		path, err := materializeGuide(rev, focused)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(path) })
		b, _ := os.ReadFile(path)
		var mg model.ReviewGuide
		if err := json.Unmarshal(b, &mg); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, f := range mg.Files {
			n += len(f.Skims)
		}
		if (n > 0) != focused || len(mg.Files) != len(rev.Guide.Files) {
			t.Errorf("materializeGuide(focused=%v): %d skims, %d files (want %d files)", focused, n, len(mg.Files), len(rev.Guide.Files))
		}
	}

	os.WriteFile(filepath.Join(dir, "f.go"), []byte("package f\n\nfunc a() {}\n\nfunc b() { work(); more() }\n"), 0o644)
	git("commit", "-q", "-am", "again")
	if out, err = runLode(t, "review", "submit", id); err != nil {
		t.Fatalf("submit: %v\n%s", err, out)
	}
	if out, err = runLode(t, "review", "show", id); err != nil || !strings.Contains(out, "STALE") {
		t.Errorf("show after an author round = %q (%v), want the guide marked stale", out, err)
	}
}
