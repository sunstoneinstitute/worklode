package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestReviewDeskConfig checks the crit config `lode review exec` writes: the
// user's keys kept, both finish hooks on `lode review import`, --anchors only
// for a document, author set.
func TestReviewDeskConfig(t *testing.T) {
	user := `{"auth_token":"tok","no_open":true,"author":"someone","hooks":{"on_finish_unresolved:diff":"inline:other"}}`
	for _, tc := range []struct {
		name, user, sidecar, hook string
	}{
		{"change, no user config", "", "", "inline:lode review import --review 7"},
		{"document, user config", user, "/tmp/d/x.anchors.json", "inline:lode review import --review 7 --anchors '/tmp/d/x.anchors.json'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := deskConfig([]byte(tc.user), 7, tc.sidecar, "bob")
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			hooks := got["hooks"].(map[string]any)
			if len(hooks) != 2 || hooks["on_finish_unresolved"] != tc.hook || hooks["on_finish_approved"] != tc.hook {
				t.Errorf("hooks = %v, want both %q and nothing else", hooks, tc.hook)
			}
			if got["author"] != "bob" {
				t.Errorf("author = %v, want bob", got["author"])
			}
			if tc.user != "" && (got["auth_token"] != "tok" || got["no_open"] != true) {
				t.Errorf("user keys lost: %v", got)
			}
		})
	}
}

// TestReviewDeskExec runs `lode review exec` against a fake crit that fires
// the desk's finish hook: a change review lands an unfocused crit round with
// the payload's thread, and a document review run --focused lands a focused
// one (WL-REQ-1776, WL-RULE-1320).
func TestReviewDeskExec(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
		t.Fatal(err)
	}
	bobToken, err := st.CreateToken(ctx, "bob", "bob token", nil)
	if err != nil {
		t.Fatal(err)
	}
	lode := buildLodeBinary(t)
	t.Setenv("PATH", filepath.Join(packageDir, "testdata", "fake-crit")+string(os.PathListSeparator)+
		filepath.Dir(lode)+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", t.TempDir()) // the desks exec makes
	if err := os.WriteFile(filepath.Join(home, ".crit.config.json"), []byte(`{"no_open":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(packageDir, "..", "review", "critjson", "testdata", "v0.23.2")
	review := func(id int64) model.Review {
		t.Helper()
		r, _, err := c.GetReview(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// A change: the fixture's comment sits on lines 6-7 of main.go.
	task := createTestTask(t, c, "Change at the desk")
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if err := gitexec.Run(dir, append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644)
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("checkout", "-q", "-b", task.Branch)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n\tfmt.Println(\"world\")\n}\n"), 0o644)
	git("add", "-A")
	git("commit", "-q", "-m", "feature")
	head, err := gitexec.Text(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.SetTaskReviewers(ctx, task.ID, []string{"bob"}); err != nil {
		t.Fatal(err)
	}
	cr, _, err := c.OpenReview(ctx, model.OpenReviewInput{Task: task.ID, BaseRef: head, AllowReady: true})
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("LODE_TOKEN", bobToken)
	os.WriteFile(filepath.Join(dir, ".crit.config.json"), []byte(`{}`), 0o644)
	t.Chdir(dir)
	if _, err := runLode(t, "review", "exec", strconv.FormatInt(cr.ID, 10)); err == nil || !strings.Contains(err.Error(), ".crit.config.json") {
		t.Fatalf("exec with a project crit config: err = %v, want it refused naming the file", err)
	}
	os.Remove(filepath.Join(dir, ".crit.config.json"))

	t.Setenv("FAKE_CRIT_FINISH", filepath.Join(fixtures, "finish_unresolved.json"))
	out, err := runLode(t, "review", "exec", strconv.FormatInt(cr.ID, 10))
	if err != nil {
		t.Fatalf("exec on a change: %v\n%s", err, out)
	}
	if !strings.Contains(out, "fake crit argv:\n") || !strings.Contains(out, "round 1 (changes_requested) landed from crit") {
		t.Errorf("exec output = %q, want crit run with no arguments and the landed round", out)
	}
	got := review(cr.ID)
	if len(got.Rounds) != 1 || got.Rounds[0].Client != "crit" || got.Rounds[0].Focused {
		t.Fatalf("change rounds = %+v, want one unfocused crit round", got.Rounds)
	}
	if len(got.Threads) != 1 || got.Threads[0].Anchor.Path != "main.go" || got.Threads[0].Anchor.Line != 6 {
		t.Errorf("change threads = %+v, want one on main.go:6", got.Threads)
	}

	// A document, read --focused.
	d, _, err := c.CreateDoc(ctx, model.CreateDocInput{Project: "proj", Kind: "spec", Number: 43, Slug: "043-desk", Body: reviewCmdSpecBody})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.SetDocReviewers(ctx, d.ID, []string{"bob"}); err != nil {
		t.Fatal(err)
	}
	dr, _, err := c.OpenReview(ctx, model.OpenReviewInput{Doc: d.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CRIT_FINISH", filepath.Join(fixtures, "finish_approved.json"))
	out, err = runLode(t, "review", "exec", strconv.FormatInt(dr.ID, 10), "--focused")
	if err != nil {
		t.Fatalf("exec on a document: %v\n%s", err, out)
	}
	if !strings.Contains(out, "/043-desk.md\n") || !strings.Contains(out, "round 1 (approved) landed from crit") {
		t.Errorf("exec output = %q, want crit run on the copy and the landed round", out)
	}
	got = review(dr.ID)
	if len(got.Rounds) != 1 || got.Rounds[0].Client != "crit" || !got.Rounds[0].Focused {
		t.Fatalf("document rounds = %+v, want one focused crit round", got.Rounds)
	}
}
