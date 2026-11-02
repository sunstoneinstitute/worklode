package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/review/critjson"
)

// TestReviewImportBothPaths drives one document review over the API and an
// identical one from a recorded crit finish through `lode review import`,
// with Jev answering the intents the API review stated: the two reviews'
// rows are equal once client, classifier, ids and timestamps are set aside
// (WL-REQ-1776, WL-REQ-1885).
func TestReviewImportBothPaths(t *testing.T) {
	finish, err := os.ReadFile(filepath.Join(packageDir, "..", "review", "critjson", "testdata", "v0.23.2", "finish_unresolved.json"))
	if err != nil {
		t.Fatal(err)
	}
	intents := map[string]string{"fix the rule": "change_request", "why two?": "question"}
	jev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			State struct {
				Threads map[string]struct {
					Body string `json:"body"`
				} `json:"threads"`
			} `json:"state"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		answers := map[string]any{}
		for id, th := range req.State.Threads {
			answers[id] = map[string]any{"choice": intents[th.Body], "confidence": 0.9}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer jev.Close()
	st, c := testServer(t, api.Config{TypeSafeAPIKey: "k", TypeSafeURL: jev.URL}, nil)
	setupProject(t, c)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
		t.Fatal(err)
	}
	bobToken, err := st.CreateToken(ctx, "bob", "bob token", nil)
	if err != nil {
		t.Fatal(err)
	}
	bob := cli.NewClient(cli.Config{ServerURL: os.Getenv("LODE_SERVER"), Token: bobToken})
	openOn := func(number int, slug string) (model.Review, model.DocDetail) {
		t.Helper()
		d, _, err := c.CreateDoc(ctx, model.CreateDocInput{Project: "proj", Kind: "spec", Number: number, Slug: slug, Body: reviewCmdSpecBody})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runLode(t, "doc", "set", "reviewers", "bob", slug); err != nil {
			t.Fatal(err)
		}
		rev, _, err := c.OpenReview(ctx, model.OpenReviewInput{Doc: d.ID})
		if err != nil {
			t.Fatal(err)
		}
		detail, _, err := c.GetDoc(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return rev, detail
	}

	// The API path: bob states both intents.
	apiRev, apiDoc := openOn(41, "041-api")
	body := string(apiDoc.Body)
	anchor := func(q string) model.ReviewAnchor {
		i := strings.Index(body, q)
		return model.ReviewAnchor{Doc: apiDoc.ID, Version: apiDoc.Version, ByteStart: i, ByteEnd: i + len(q), Quote: q}
	}
	if _, _, err := bob.SubmitReviewRound(ctx, apiRev.ID, model.ReviewRoundInput{Verdict: "changes_requested", Client: "agent",
		Threads: []model.OpenThreadInput{
			{Anchor: anchor("rule body"), Intent: "change_request", Body: "fix the rule"},
			{Anchor: anchor("Second body."), Intent: "question", Body: "why two?"},
		}}); err != nil {
		t.Fatalf("API round: %v", err)
	}

	// The crit path: the same comments on the same lines of the document
	// `lode doc show --anchors` wrote, piped through `lode review import`.
	critRev, _ := openOn(42, "042-crit")
	dir := t.TempDir()
	side, copyPath := filepath.Join(dir, "doc.anchors.json"), filepath.Join(dir, "doc.md")
	t.Setenv("LODE_TOKEN", bobToken)
	if out, err := runLode(t, "doc", "show", "042-crit", "--anchors", side, "--out", copyPath); err != nil {
		t.Fatalf("doc show --anchors: %v\n%s", err, out)
	}
	written, _ := os.ReadFile(copyPath)
	lineOf := func(s string) int {
		for i, l := range strings.Split(string(written), "\n") {
			if strings.Contains(l, s) {
				return i + 1
			}
		}
		t.Fatalf("no line holds %q", s)
		return 0
	}
	var f critjson.Finish
	if err := json.Unmarshal(finish, &f); err != nil {
		t.Fatal(err)
	}
	path := "doc.md"
	comment := func(id, quote, text string) critjson.ListedComment {
		n := lineOf(quote)
		return critjson.ListedComment{Scope: "line", Path: &path, Comment: critjson.Comment{
			ID: id, StartLine: n, EndLine: n, Body: text, Quote: quote, CreatedAt: "2026-11-02T19:16:09Z", UpdatedAt: "2026-11-02T19:16:09Z"}}
	}
	f.Mode, f.CommentsJSON = "files", []critjson.ListedComment{comment("c_1", "rule body", "fix the rule"), comment("c_2", "Second body.", "why two?")}
	f.CommentsUnresolvedJSON, f.UnresolvedCount, f.TotalCount = f.CommentsJSON, 2, 2
	payload, _ := json.Marshal(f)
	rootCmd.SetIn(strings.NewReader(string(payload)))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	out, err := runLode(t, "review", "import", "--review", strconv.FormatInt(critRev.ID, 10), "--anchors", side)
	if err != nil {
		t.Fatalf("review import: %v\n%s", err, out)
	}
	if !strings.Contains(out, "round 1 (changes_requested): 2 threads opened, 0 comments matched") {
		t.Errorf("import output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "finish.json")); err != nil {
		t.Errorf("finish.json not saved beside the sidecar: %v", err)
	}
	// Re-running the hook opens nothing new.
	rootCmd.SetIn(strings.NewReader(string(payload)))
	out, err = runLode(t, "review", "import", "--review", strconv.FormatInt(critRev.ID, 10), "--anchors", side)
	if err != nil || !strings.Contains(out, "0 threads opened, 2 comments matched existing threads") {
		t.Errorf("re-run = %q, %v; want no new thread", out, err)
	}

	got := func(id int64, client, classifier string) string {
		t.Helper()
		r, _, err := c.GetReview(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Rounds) == 0 || r.Rounds[0].Client != client {
			t.Errorf("review %d rounds = %+v, want client %s", id, r.Rounds, client)
		}
		var rows []string
		for _, th := range r.Threads {
			if th.IntentClassifier != classifier {
				t.Errorf("review %d thread %d classifier %q, want %q", id, th.ID, th.IntentClassifier, classifier)
			}
			a := th.Anchor
			var msgs []string
			for _, m := range th.Messages {
				msgs = append(msgs, m.Role+":"+m.Body)
			}
			rows = append(rows, strings.Join([]string{strconv.FormatBool(a.Rule != 0), strconv.Itoa(a.Version),
				strconv.Itoa(a.ByteStart), strconv.Itoa(a.ByteEnd), a.Quote, a.DocAnchor, th.Intent, th.Status, strings.Join(msgs, "|")}, " "))
		}
		sort.Strings(rows)
		return r.State + " " + r.Rounds[0].Verdict + "\n" + strings.Join(rows, "\n")
	}
	apiRows, critRows := got(apiRev.ID, "agent", ""), got(critRev.ID, "crit", "jev")
	if apiRows != critRows {
		t.Errorf("the two reviewer paths differ:\nAPI:\n%s\ncrit:\n%s", apiRows, critRows)
	}
	if !strings.Contains(apiRows, "true") {
		t.Errorf("no thread landed on a rule: %s", apiRows)
	}
}
