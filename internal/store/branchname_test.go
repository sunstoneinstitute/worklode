package store

import (
	"regexp"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestSetBranchTemplateValid(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { SetBranchTemplate("") })
	cases := []struct{ name, tmpl, want string }{
		{"default", "", "WL-7-fix-the-thing"},
		{"explicit default", DefaultBranchTemplate, "WL-7-fix-the-thing"},
		{"namespaced", "lode/{{ .id }}-{{ .slug }}", "lode/WL-7-fix-the-thing"},
		{"id only", "{{ .id }}", "WL-7"},
		{"projectId", "{{ .projectId }}/{{ .id }}-{{ .slug }}", "worklode/WL-7-fix-the-thing"},
		{"kind", "{{ .kind }}/{{ .id }}-{{ .slug }}", "feature/WL-7-fix-the-thing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := SetBranchTemplate(c.tmpl); err != nil {
				t.Fatalf("SetBranchTemplate(%q) = %v, want nil", c.tmpl, err)
			}
			task := &model.Task{ID: "WL-7", Title: "Fix the thing", Project: "worklode", Kind: "feature"}
			if got := BranchFor(task); got != c.want {
				t.Errorf("BranchFor = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSetBranchTemplateRejects also covers the "rejected template leaves the
// previous configuration in place" invariant (spec 008 §3.2): it configures a
// known-good, non-default template up front, then after every rejection
// checks that BranchTemplate() (and TaskIDFromRef, at least once) still
// reflect that good template rather than the rejected one or the default.
func TestSetBranchTemplateRejects(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { SetBranchTemplate("") })
	const goodTmpl = "lode/{{ .id }}-{{ .slug }}"
	if err := SetBranchTemplate(goodTmpl); err != nil {
		t.Fatalf("SetBranchTemplate(%q) = %v, want nil", goodTmpl, err)
	}
	goodBranch := BranchFor(&model.Task{ID: "WL-7", Title: "Fix the thing"})

	cases := []struct{ name, tmpl string }{
		{"unparseable", "{{ .id "},
		{"unknown field", "{{ .nope }}-{{ .id }}"},
		{"no id reference", "{{ .slug }}"},
		{"empty render", "{{ if false }}{{ .id }}{{ end }}"},
		{"space", "{{ .id }} {{ .slug }}"},
		{"double dot", "{{ .id }}..{{ .slug }}"},
		{"double slash", "a//{{ .id }}"},
		{"leading slash", "/{{ .id }}"},
		{"trailing slash", "{{ .id }}/"},
		{"tilde", "~{{ .id }}"},
		{"caret", "^{{ .id }}"},
		{"colon", "a:{{ .id }}"},
		{"question", "{{ .id }}?"},
		{"star", "{{ .id }}*"},
		{"bracket", "{{ .id }}["},
		{"backslash", `{{ .id }}\x`},
		{"at-brace", "{{ .id }}@{x"},
		{"lock suffix", "{{ .id }}.lock"},
		{"dot-leading component", ".{{ .id }}"},
		{"dot-trailing component", "{{ .id }}./x"},
		{"control char", "{{ .id }}\x01"},
		// .project was rejected as a field name (ambiguous: id vs name vs
		// key) in favor of the explicit .projectId; missingkey=error makes
		// the old name an unknown field like any other typo.
		{"old .project field name", "{{ .project }}/{{ .id }}-{{ .slug }}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := SetBranchTemplate(c.tmpl); err == nil {
				t.Errorf("SetBranchTemplate(%q) = nil, want error", c.tmpl)
			}
			if got := BranchTemplate(); got != goodTmpl {
				t.Errorf("after rejecting %q: BranchTemplate() = %q, want previous %q", c.tmpl, got, goodTmpl)
			}
		})
	}

	// The previous template's correlation pattern must also still be live —
	// not just the text SetBranchTemplate reports.
	if got := TaskIDFromRef(goodBranch); got != "WL-7" {
		t.Errorf("after rejections: TaskIDFromRef(%q) = %q, want WL-7 (previous template still installed)", goodBranch, got)
	}
}

// TestBranchRoundTrip is the property that makes the derived pattern
// trustworthy: whatever the template, a branch it renders must parse back to
// the id it was rendered from.
func TestBranchRoundTrip(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { SetBranchTemplate("") })
	tmpls := []string{
		DefaultBranchTemplate,
		"lode/{{ .id }}-{{ .slug }}",
		"{{ .projectId }}/{{ .id }}-{{ .slug }}",
		"{{ .kind }}/{{ .id }}",
		"{{ .id }}",
	}
	tasks := []*model.Task{
		{ID: "WL-7", Title: "Fix the thing", Project: "worklode", Kind: "feature"},
		{ID: "SW-1234", Title: "A much longer title that will be truncated somewhere", Project: "sw", Kind: "bug"},
		{ID: "X9-1", Title: "!!!", Project: "p", Kind: "chore"},
	}
	for _, tmpl := range tmpls {
		if err := SetBranchTemplate(tmpl); err != nil {
			t.Fatalf("SetBranchTemplate(%q) = %v", tmpl, err)
		}
		for _, task := range tasks {
			branch := BranchFor(task)
			if got := TaskIDFromRef(branch); got != task.ID {
				t.Errorf("template %q: TaskIDFromRef(%q) = %q, want %q", tmpl, branch, got, task.ID)
			}
		}
	}
}

// TestBranchForSanitizesProjectID covers spec 008 §3.1's render-time
// sanitization: projects.id is free text (no ref-safety guarantee), so
// SetBranchTemplate's sample-render validation cannot catch a real project id
// that would render an illegal branch. BranchFor must run .projectId (like
// .slug) through SlugifyTitle before rendering.
func TestBranchForSanitizesProjectID(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate("{{ .projectId }}/{{ .id }}-{{ .slug }}"); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{ID: "WL-7", Title: "Fix the thing", Project: "my project"}
	const want = "my-project/WL-7-fix-the-thing"
	branch := BranchFor(task)
	if branch != want {
		t.Fatalf("BranchFor = %q, want %q", branch, want)
	}
	if err := validateRef(branch); err != nil {
		t.Errorf("BranchFor produced an illegal ref %q: %v", branch, err)
	}
	if got := TaskIDFromRef(branch); got != task.ID {
		t.Errorf("TaskIDFromRef(%q) = %q, want %q", branch, got, task.ID)
	}
}

func TestTaskIDFromRefRejects(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate(""); err != nil {
		t.Fatal(err)
	}
	// Legacy prefixes are gone (spec 008 §7); a lowercase id never matches;
	// a bare id has no slug separator under the default template; trailing
	// garbage after a would-be match must not match either (derivePattern's
	// "$" anchor, not just "^").
	for _, ref := range []string{"lode/WL-7-x", "wl/WL-7-x", "wl-7-x", "main", "WL-7", "feature/WL-7-x", "WL-7-x/extra"} {
		if got := TaskIDFromRef(ref); got != "" {
			t.Errorf("TaskIDFromRef(%q) = %q, want \"\"", ref, got)
		}
	}
}

func TestDerivedPatternEscapesLiterals(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate("a.b-{{ .id }}-{{ .slug }}"); err != nil {
		t.Fatal(err)
	}
	// The "." is a literal, not a wildcard.
	if got := TaskIDFromRef("axb-WL-7-x"); got != "" {
		t.Errorf("TaskIDFromRef(\"axb-WL-7-x\") = %q, want \"\" (dot must be literal)", got)
	}
	if got := TaskIDFromRef("a.b-WL-7-x"); got != "WL-7" {
		t.Errorf("TaskIDFromRef(\"a.b-WL-7-x\") = %q, want WL-7", got)
	}
}

func TestBranchTemplateReportsCurrent(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate("lode/{{ .id }}-{{ .slug }}"); err != nil {
		t.Fatal(err)
	}
	if got := BranchTemplate(); got != "lode/{{ .id }}-{{ .slug }}" {
		t.Errorf("BranchTemplate() = %q", got)
	}
}

// guard against the sentinel leaking into the compiled pattern. Uses a
// template that references all four fields (not just the default's .id and
// .slug) so a missed substitution for .projectId or .kind would also be
// caught.
func TestDerivedPatternHasNoSentinel(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate("{{ .kind }}/{{ .projectId }}/{{ .id }}-{{ .slug }}"); err != nil {
		t.Fatal(err)
	}
	branchMu.RLock()
	src := branchPattern.String()
	branchMu.RUnlock()
	if strings.Contains(src, "\x00") {
		t.Errorf("derived pattern still contains a sentinel: %q", src)
	}
	if _, err := regexp.Compile(src); err != nil {
		t.Errorf("derived pattern does not compile: %v", err)
	}
}
