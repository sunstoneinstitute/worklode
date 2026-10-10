package gate

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

func texts(cs []Citation) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Text)
	}
	return out
}

func TestCitationsMatches(t *testing.T) {
	cases := map[string][]string{
		"see WL-SPEC-77 §4 for this":            {"WL-SPEC-77 §4"},
		"see WL-SPEC-77 sec-4.1d.":              {"WL-SPEC-77 sec-4.1d"},
		"read WL-SPEC-80#sec-4.1d first":        {"WL-SPEC-80#sec-4.1d"},
		"jump to #sec-3":                        {"#sec-3"},
		"as §12 says":                           {"§12"},
		"as § 3.2 says":                         {"§ 3.2"},
		"WL-REQ-165 is the rule":                nil,
		"WL-REQ-165 §2 is part of a rule ref":   nil,
		"the sign § alone":                      nil,
		"a link https://x.test/WL-SPEC-1#sec-2": nil,
		"```\nWL-SPEC-77 §4\n```\nafter":        nil,
		"one WL-SPEC-77 §4 and §9":              {"WL-SPEC-77 §4", "§9"},
	}
	for in, want := range cases {
		got := texts(Citations(in))
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("Citations(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCitationsSection(t *testing.T) {
	cs := Citations("WL-SPEC-77 §4 and WL-SPEC-80#sec-4.1d and §5")
	if len(cs) != 3 {
		t.Fatalf("got %d citations: %+v", len(cs), cs)
	}
	if s := cs[0].Section; s == nil || s.Shorthand.Number != 77 || s.Anchor != "sec-4" {
		t.Errorf("first section = %+v", s)
	}
	if s := cs[1].Section; s == nil || s.Shorthand.Number != 80 || s.Anchor != "sec-4.1d" {
		t.Errorf("second section = %+v", s)
	}
	if cs[2].Section != nil {
		t.Errorf("a bare section sign names no document: %+v", cs[2].Section)
	}
}

func TestDescribeNamesRuleOrFallback(t *testing.T) {
	cs := Citations("WL-SPEC-77 §4, WL-SPEC-80 §9 and §5")
	resolve := func(s designdoc.SectionRef) string {
		if s.Shorthand.Number == 77 && s.Anchor == "sec-4" {
			return "WL-REQ-165"
		}
		return ""
	}
	got := Describe(cs, resolve)
	for _, want := range []string{"cite WL-REQ-165", "lode show WL-SPEC-80#sec-9", "lode show <doc>#sec-N"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe = %q, missing %q", got, want)
		}
	}
}

func TestAddedCitations(t *testing.T) {
	cfg := Config{Paths: []string{"x"}, Exempt: []string{"internal/designdoc/**", "**/testdata/**"}}
	diff := strings.Join([]string{
		"diff --git a/a.go b/a.go",
		"--- a/a.go",
		"+++ b/a.go",
		"@@ -1,2 +1,5 @@",
		" package a",
		`+var s = "#sec-"`,
		"+// read WL-SPEC-77 §4 here",
		`+x := "§3" // fine`,
		"-// WL-SPEC-1 §1 removed lines are not checked",
		"diff --git a/doc.md b/doc.md",
		"--- a/doc.md",
		"+++ b/doc.md",
		"@@ -10,0 +11,4 @@",
		"+see #sec-2",
		"+```",
		"+WL-SPEC-1 §1",
		"+```",
		"diff --git a/internal/designdoc/x.go b/internal/designdoc/x.go",
		"--- a/internal/designdoc/x.go",
		"+++ b/internal/designdoc/x.go",
		"@@ -1,0 +1,1 @@",
		"+// WL-SPEC-1 §1 exempt",
	}, "\n")
	got, err := cfg.AddedCitations(diff)
	if err != nil {
		t.Fatal(err)
	}
	var where []string
	for _, c := range got {
		where = append(where, c.Where+" "+c.Text)
	}
	want := []string{"a.go:3 WL-SPEC-77 §4", "doc.md:11 #sec-2"}
	if strings.Join(where, "|") != strings.Join(want, "|") {
		t.Errorf("AddedCitations = %q, want %q", where, want)
	}
}

func TestParseExempt(t *testing.T) {
	cfg, ok, err := Parse([]byte("[gate]\npaths = [\"a/**\"]\nexempt = [\"b/**\"]\n"))
	if err != nil || !ok || len(cfg.Exempt) != 1 {
		t.Fatalf("Parse = %+v %v %v", cfg, ok, err)
	}
}
