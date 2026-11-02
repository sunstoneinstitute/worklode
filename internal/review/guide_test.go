package review_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/review"
)

func fixture(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func span(from, to int) []model.GuideSpan { return []model.GuideSpan{{From: from, To: to}} }

func TestMechanical(t *testing.T) {
	for _, c := range []struct {
		name, path, hunk, rule string
		ok                     bool
	}{
		{"lockfile", "web/pnpm-lock.yaml", "", "lockfile", true},
		{"templ", "a/b_templ.go", "", "generated", true},
		{"generated header", "a/b.go", "generated.hunk", "generated", true},
		{"vendored", "vendor/x/y.go", "", "vendored", true},
		{"snapshot", "a/testdata/x.golden", "", "snapshot", true},
		{"go under testdata", "a/testdata/x.go", "edited.hunk", "non-mechanical", false},
		{"import-only", "a/b.go", "import-only.hunk", "import-only", true},
		{"format-only", "a/b.go", "format-only.hunk", "format-only", true},
		{"edited", "a/b.go", "edited.hunk", "non-mechanical", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var hunks []string
			if c.hunk != "" {
				hunks = []string{fixture(t, "hunks", c.hunk)}
			}
			ok, rule := review.Mechanical(c.path, hunks)
			if ok != c.ok || rule != c.rule {
				t.Fatalf("got %v %q, want %v %q", ok, rule, c.ok, c.rule)
			}
		})
	}
}

func TestSplitDiff(t *testing.T) {
	m := review.SplitDiff(fixture(t, "mixed.diff"))
	if len(m) != 3 || len(m["internal/x/x.go"]) != 1 {
		t.Fatalf("got %v", m)
	}
}

func TestComposeHonesty(t *testing.T) {
	p := model.GuideProposal{
		Files: []model.GuideFile{
			{Path: "go.sum", Skims: span(1, 40)},
			{Path: "internal/x/x.go", Skims: span(10, 20)},
			{Path: "internal/y/y_templ.go"},
		},
		Diff: fixture(t, "mixed.diff"),
	}
	g, refused := review.Compose(p, true)
	if want := []review.Refusal{{"internal/x/x.go", "non-mechanical"}}; !reflect.DeepEqual(refused, want) {
		t.Fatalf("refused %v", refused)
	}
	if s := g.Files[0].Skims; len(s) != 1 || s[0].DecidedBy != "author" || s[0].To != 40 {
		t.Fatalf("go.sum %v", s)
	}
	if len(g.Files[1].Skims) != 0 {
		t.Fatalf("x.go %v", g.Files[1].Skims)
	}
	if s := g.Files[2].Skims; len(s) != 1 || s[0].DecidedBy != "policy" {
		t.Fatalf("y_templ %v", s)
	}
	g, _ = review.Compose(p, false)
	for _, f := range g.Files {
		if len(f.Skims) != 0 {
			t.Fatalf("unfocused kept skims on %s", f.Path)
		}
	}
}

func TestComposeNoDiff(t *testing.T) {
	_, refused := review.Compose(model.GuideProposal{Files: []model.GuideFile{{Path: "a.go", Skims: span(1, 2)}}}, true)
	if len(refused) != 1 || refused[0].Rule != "no-diff" {
		t.Fatalf("%v", refused)
	}
}
