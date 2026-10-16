package cli_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestProjectDocumentTable(t *testing.T) {
	var b strings.Builder
	cli.ProjectDocumentTable(&b, []model.ProjectDocument{
		{ID: "doc1", Name: "Notes", Category: "sources", Type: "document", Status: "draft"},
	})
	out := b.String()
	for _, want := range []string{"CATEGORY", "doc1", "Notes", "sources", "draft"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(lineWith(t, out, "doc1"), "-") {
		t.Fatalf("empty url should show a dash:\n%s", out)
	}
}
