//go:build e2e

package e2e

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestRuleDeriveEndToEnd: `lode rule derive --dry-run` and `--diff` report a
// URL reference's rewrite and write nothing, then a real run rewrites the
// rule and the task it names shows the row (WL-REQ-1860).
func TestRuleDeriveEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{ID: "dv", Name: "Derive", Key: "DV"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, _, err := admin.CreateTask(ctx, model.CreateTaskInput{Project: "dv", Title: "Target", Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	rule, _, err := admin.AddRule(ctx, model.AddRuleInput{Project: "dv", Heading: model.Markdown("Names a task"), Body: model.Markdown("See /" + task.ID + " first.")})
	if err != nil {
		t.Fatalf("add rule: %v", err)
	}
	ref := "DV-REQ-1"
	if rule.Number != 1 {
		t.Fatalf("rule number %d", rule.Number)
	}

	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", bootstrapToken)
	body := func() string {
		t.Helper()
		r, _, err := admin.GetRule(ctx, ref)
		if err != nil {
			t.Fatalf("get rule: %v", err)
		}
		return string(r.Body)
	}
	before := body()
	if !strings.Contains(before, "/"+task.ID) {
		t.Fatalf("seed body = %q", before)
	}

	for _, flag := range []string{"--dry-run", "--diff"} {
		out, err := runLodeCLI(t, "rule", "derive", ref, "--project", "dv", flag)
		if err != nil {
			t.Fatalf("rule derive %s: %v\n%s", flag, err, out)
		}
		if flag == "--diff" && !strings.Contains(out, "+See "+task.ID+" first.") {
			t.Errorf("diff output = %q", out)
		}
		if got := body(); got != before {
			t.Fatalf("%s wrote: %q -> %q", flag, before, got)
		}
	}

	out, err := runLodeCLI(t, "rule", "derive", ref, "--project", "dv",
		"--dry-run=false", "--diff=false") // flag values persist across in-process runs
	if err != nil {
		t.Fatalf("rule derive: %v\n%s", err, out)
	}
	if got := body(); !strings.Contains(got, "See "+task.ID+" first.") || strings.Contains(got, "/"+task.ID) {
		t.Errorf("body after derive = %q", got)
	}
	detail, _, err := admin.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if len(detail.InlineRefs.In) != 1 || detail.InlineRefs.In[0].FromRef != ref {
		t.Errorf("task inline_refs_in = %+v", detail.InlineRefs.In)
	}
}
