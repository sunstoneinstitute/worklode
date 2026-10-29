package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestPagesLinkToTheBodyEditor: the document, rule and task pages link at
// their SPA edit route (WL-REQ-1868); a spec, whose text is its rules', and
// an old rule version get none.
func TestPagesLinkToTheBodyEditor(t *testing.T) {
	html := func(t *testing.T, r func(*strings.Builder) error) string {
		t.Helper()
		var b strings.Builder
		if err := r(&b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	ctx := context.Background()
	plan := html(t, func(b *strings.Builder) error {
		return Doc(DocView{Doc: model.Doc{ID: 3, Project: "web", Kind: "plan", Number: 5}}).Render(ctx, b)
	})
	spec := html(t, func(b *strings.Builder) error {
		return Doc(DocView{Doc: model.Doc{ID: 4, Project: "web", Kind: "spec", Number: 89}}).Render(ctx, b)
	})
	rule := html(t, func(b *strings.Builder) error {
		return Rule(RuleView{Current: true, CanonicalURL: "/projects/web/rule/7"}).Render(ctx, b)
	})
	old := html(t, func(b *strings.Builder) error {
		return Rule(RuleView{CanonicalURL: "/projects/web/rule/7"}).Render(ctx, b)
	})
	task := html(t, func(b *strings.Builder) error {
		return Task(TaskView{Project: CockpitProject{Key: "WEB"}, Task: model.Task{ID: "WEB-9", Project: "web", Kind: "feature"}}).Render(ctx, b)
	})
	for name, c := range map[string]struct {
		page, want string
	}{
		"plan": {plan, `href="/projects/web/plan/5/edit"`},
		"rule": {rule, `href="/projects/web/rule/7/edit"`},
		"task": {task, `href="/projects/web/feature/9/edit"`},
	} {
		if !strings.Contains(c.page, c.want) {
			t.Errorf("%s page lacks %s", name, c.want)
		}
	}
	for name, page := range map[string]string{"spec": spec, "old rule version": old} {
		if strings.Contains(page, "/edit\"") {
			t.Errorf("%s page links to the editor", name)
		}
	}
}
