//go:build e2e

package e2e

import (
	"context"
	"net/http/httptest"
	"strconv"
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
	rule, _, err := admin.AddRule(ctx, model.AddRuleInput{Project: "dv", Heading: model.Markdown("Names a task"), Body: model.Markdown("See https://lode.example/" + task.ID + " first.")})
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
	if got := body(); !strings.Contains(got, "See "+task.ID+" first.") || strings.Contains(got, "lode.example") {
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

// TestRuleShowInlineRefsEndToEnd: `lode rule show` lists a rule's references to
// a task, a doc and a milestone, and a task's reference to the rule, in human
// output and --json (WL-REQ-1852).
func TestRuleShowInlineRefsEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{ID: "rs", Name: "Show", Key: "RS"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, _, err := admin.CreateTask(ctx, model.CreateTaskInput{Project: "rs", Title: "Target", Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	doc, _, err := admin.CreateDoc(ctx, model.CreateDocInput{Project: "rs", Kind: "plan", Slug: "named", Body: model.Markdown("# Named\n")})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	mile, _, err := admin.CreateMilestone(ctx, "rs", model.CreateMilestoneInput{Title: "Named"})
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}
	docRef := "RS-PLAN-" + strconv.Itoa(doc.Number)
	if _, _, err := admin.AddRule(ctx, model.AddRuleInput{Project: "rs", Heading: model.Markdown("Names three"),
		Body: model.Markdown("See " + task.ID + ", " + docRef + " and " + mile.ID + ".")}); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	src, _, err := admin.CreateTask(ctx, model.CreateTaskInput{Project: "rs", Title: "Source", Priority: "high", Kind: "feature", Body: "Governed by RS-REQ-1."})
	if err != nil {
		t.Fatalf("create source task: %v", err)
	}

	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", bootstrapToken)
	for _, args := range [][]string{{"rule", "show", "RS-REQ-1"}, {"rule", "show", "RS-REQ-1", "--json"}} {
		out, err := runLodeCLI(t, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		for _, want := range []string{task.ID, docRef, mile.ID, src.ID} {
			if !strings.Contains(out, want) {
				t.Errorf("%v output lacks %s:\n%s", args, want, out)
			}
		}
	}
}
