package cli_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestIdeaTable(t *testing.T) {
	var b strings.Builder
	cli.IdeaTable(&b, []model.Idea{
		{ID: "idea_1", Title: "Rivers", Type: "general", Status: "private", Author: "bob", Submitted: "2026-10-18"},
	})
	out := b.String()
	for _, want := range []string{"STATUS", "idea_1", "Rivers", "private", "bob", "2026-10-18"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(lineWith(t, out, "idea_1"), "-") {
		t.Fatalf("an unshared idea's task should show a dash:\n%s", out)
	}
}
