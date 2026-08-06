package designdoc

import (
	"os"
	"reflect"
	"testing"
)

// TestParseFrontmatterRealSpec uses spec 025 because it is the one document
// exercising every shape at once: scalars, a list, and two anchor-keyed maps.
func TestParseFrontmatterRealSpec(t *testing.T) {
	src, err := os.ReadFile("../../docs/specs/025-documents-in-the-backbone.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fm := doc.Frontmatter
	if fm == nil {
		t.Fatal("Frontmatter is nil")
	}
	if fm.Status != "accepted" {
		t.Errorf("Status = %q, want accepted", fm.Status)
	}
	if fm.Issued != "2026-08-02" {
		t.Errorf("Issued = %q, want 2026-08-02", fm.Issued)
	}
	wantRequires := []string{
		"004-execution-backbone.md",
		"006-knowledge-graph.md",
		"014-design-documents-as-graph-objects.md",
		"018-task-hierarchy.md",
	}
	if !reflect.DeepEqual([]string(fm.Requires), wantRequires) {
		t.Errorf("Requires = %v, want %v", fm.Requires, wantRequires)
	}
	if got := []string(fm.Amends["#sec-3"]); !reflect.DeepEqual(got, []string{"014-design-documents-as-graph-objects.md#sec-5"}) {
		t.Errorf("Amends[#sec-3] = %v", got)
	}
	if got := len(fm.Amends["#sec-8"]); got != 3 {
		t.Errorf("Amends[#sec-8] has %d refs, want 3", got)
	}
	if got := []string(fm.Replaces["#sec-6"]); !reflect.DeepEqual(got, []string{"014-design-documents-as-graph-objects.md#sec-8"}) {
		t.Errorf("Replaces[#sec-6] = %v", got)
	}
	// The H1 must not be swallowed into the frontmatter block.
	if doc.Preamble == "" || doc.Sections[0].Anchor != "sec-0" {
		t.Errorf("Preamble = %q, first anchor = %q", doc.Preamble, doc.Sections[0].Anchor)
	}
}

// TestFrontmatterScalarOrList covers implements, which the authoring guide
// documents as "scalar or list" — both spellings must land in the same field.
func TestFrontmatterScalarOrList(t *testing.T) {
	scalar := "---\nimplements: docs/specs/011-delivery-lifecycle.md\n---\n\n## 1. X {#sec-1}\n"
	list := "---\nimplements:\n  - a.md\n  - b.md\n---\n\n## 1. X {#sec-1}\n"

	doc, err := Parse([]byte(scalar))
	if err != nil {
		t.Fatalf("Parse scalar: %v", err)
	}
	if want := []string{"docs/specs/011-delivery-lifecycle.md"}; !reflect.DeepEqual([]string(doc.Frontmatter.Implements), want) {
		t.Errorf("scalar Implements = %v, want %v", doc.Frontmatter.Implements, want)
	}

	doc, err = Parse([]byte(list))
	if err != nil {
		t.Fatalf("Parse list: %v", err)
	}
	if want := []string{"a.md", "b.md"}; !reflect.DeepEqual([]string(doc.Frontmatter.Implements), want) {
		t.Errorf("list Implements = %v, want %v", doc.Frontmatter.Implements, want)
	}
}

func TestNoFrontmatter(t *testing.T) {
	src := "# Title\n\n## 1. X {#sec-1}\n\nBody.\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Frontmatter != nil {
		t.Errorf("Frontmatter = %+v, want nil", doc.Frontmatter)
	}
	if got := string(doc.Bytes()); got != src {
		t.Errorf("round-trip differs")
	}
}

// TestUnterminatedFrontmatter guards the failure mode where a missing closing
// "---" turns the whole document body into candidate keys.
func TestUnterminatedFrontmatter(t *testing.T) {
	src := "---\nstatus: draft\n\n# Title\n\n## 1. X {#sec-1}\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Frontmatter != nil {
		t.Errorf("Frontmatter = %+v, want nil for unterminated block", doc.Frontmatter)
	}
	if got := string(doc.Bytes()); got != src {
		t.Errorf("round-trip differs")
	}
}

func TestMalformedFrontmatterIsAnError(t *testing.T) {
	src := "---\nstatus: [unclosed\n---\n\n## 1. X {#sec-1}\n"
	if _, err := Parse([]byte(src)); err == nil {
		t.Error("Parse succeeded on malformed YAML, want error")
	}
}
