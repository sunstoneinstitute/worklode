package designdoc

import (
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
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
	// Status moves as the document is accepted; pin the parsed shape
	// (a recognised value), not the current lifecycle state.
	switch fm.Status {
	case "draft", "accepted", "superseded":
	default:
		t.Errorf("Status = %q, want one of draft/accepted/superseded", fm.Status)
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

// TestFrontmatterCoverage protects the plan coverage API: scalars mean full
// coverage, objects retain their qualifiers, and the retired spelling remains
// readable only when the current spelling is absent. Removing scalar handling,
// object fields, or the precedence rule would make this test fail.
func TestFrontmatterCoverage(t *testing.T) {
	section := "docs/specs/033-plan-section-coverage.md#sec-3"
	tests := []struct {
		name     string
		src      string
		want     CoverageList
		sections RefList
	}{
		{
			name:     "scalar covers means full coverage",
			src:      "---\ncovers: " + section + "\n---\n\n## 1. X {#sec-1}\n",
			want:     CoverageList{{Spec: section, Coverage: "full"}},
			sections: RefList{section},
		},
		{
			name: "mixed scalar and object covers",
			src: "---\ncovers:\n  - docs/specs/033-plan-section-coverage.md#sec-2\n  - spec: " + section + "\n" +
				"    coverage: partial\n    fullCoverageWith:\n      - docs/plans/sibling.md\n---\n\n## 1. X {#sec-1}\n",
			want: CoverageList{
				{Spec: "docs/specs/033-plan-section-coverage.md#sec-2", Coverage: "full"},
				{Spec: section, Coverage: "partial", FullCoverageWith: RefList{"docs/plans/sibling.md"}},
			},
			sections: RefList{"docs/specs/033-plan-section-coverage.md#sec-2", section},
		},
		{
			name: "retired implements accepts objects",
			src: "---\nimplements:\n  - spec: " + section + "\n" +
				"    coverage: partial\n    fullCoverageWith: [docs/plans/sibling.md]\n---\n\n## 1. X {#sec-1}\n",
			want:     CoverageList{{Spec: section, Coverage: "partial", FullCoverageWith: RefList{"docs/plans/sibling.md"}}},
			sections: RefList{section},
		},
		{
			name:     "covers takes precedence when both keys appear",
			src:      "---\ncovers: " + section + "\nimplements: docs/specs/033-plan-section-coverage.md#sec-4\n---\n\n## 1. X {#sec-1}\n",
			want:     CoverageList{{Spec: section, Coverage: "full"}},
			sections: RefList{section},
		},
		{
			name:     "explicit empty covers takes precedence over implements",
			src:      "---\ncovers: []\nimplements: " + section + "\n---\n\n## 1. X {#sec-1}\n",
			want:     CoverageList{},
			sections: RefList{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := doc.Frontmatter.CoverageEntries(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("CoverageEntries() = %#v, want %#v", got, tc.want)
			}
			if got := doc.Frontmatter.CoveredSections(); !reflect.DeepEqual(got, tc.sections) {
				t.Errorf("CoveredSections() = %#v, want %#v", got, tc.sections)
			}
		})
	}
}

func TestFrontmatterCoverageRejectsMalformedObjects(t *testing.T) {
	section := "docs/specs/033-plan-section-coverage.md#sec-3"
	tests := []struct {
		name  string
		entry string
	}{
		{name: "spec sequence", entry: "spec: []\n    coverage: partial"},
		{name: "coverage mapping", entry: "spec: " + section + "\n    coverage: {bad: value}"},
		{name: "completion mapping", entry: "spec: " + section + "\n    coverage: partial\n    fullCoverageWith: {plan: x}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := "---\ncovers:\n  - " + tc.entry + "\n---\n\n## 1. X {#sec-1}\n"
			if _, err := Parse([]byte(src)); err == nil {
				t.Fatal("Parse succeeded, want decode error")
			}
		})
	}

	// CoverageList itself accepts a scalar or sequence; mappings are coverage
	// entries only within that sequence. Accepting this shape would make a
	// malformed list silently look valid.
	src := "---\ncovers: {spec: " + section + ", coverage: partial}\n---\n\n## 1. X {#sec-1}\n"
	if _, err := Parse([]byte(src)); err == nil {
		t.Fatal("Parse succeeded for mapping covers value, want decode error")
	}
}

// TestFrontmatterCoverageRerendersEditedMappings ensures a changed mapping
// encodes its qualifiers instead of silently flattening them to references.
func TestFrontmatterCoverageRerendersEditedMappings(t *testing.T) {
	src := "---\ncovers:\n  - spec: docs/specs/033-plan-section-coverage.md#sec-3\n    coverage: partial\n    fullCoverageWith:\n      - docs/plans/sibling.md\n---\n\n## 1. X {#sec-1}\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Frontmatter.Covers[0].FullCoverageWith = append(doc.Frontmatter.Covers[0].FullCoverageWith, "docs/plans/other.md")
	roundTripped, err := Parse(doc.Bytes())
	if err != nil {
		t.Fatalf("Parse edited document: %v", err)
	}
	want := CoverageList{{
		Spec:             "docs/specs/033-plan-section-coverage.md#sec-3",
		Coverage:         "partial",
		FullCoverageWith: RefList{"docs/plans/sibling.md", "docs/plans/other.md"},
	}}
	if got := roundTripped.Frontmatter.CoverageEntries(); !reflect.DeepEqual(got, want) {
		t.Errorf("round-tripped CoverageEntries() = %#v, want %#v", got, want)
	}
}

// TestFrontmatterNoSpecRemainsBareWhenOtherFieldChanges catches the renderer
// turning the reserved scalar sentinel into a qualified mapping, which
// secmeta rejects because NO-SPEC has no section or coverage level to qualify.
func TestFrontmatterNoSpecRemainsBareWhenOtherFieldChanges(t *testing.T) {
	src := "---\nstatus: draft\ncovers: NO-SPEC\n---\n\n# Plan\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Frontmatter.Status = "accepted"

	rendered := doc.Bytes()
	front, inner, _ := splitFrontmatter(string(rendered))
	if front == "" {
		t.Fatal("rendered document has no frontmatter")
	}
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(inner), &fields); err != nil {
		t.Fatalf("decode rendered frontmatter: %v", err)
	}
	if got, ok := fields["covers"].(string); !ok || got != "NO-SPEC" {
		t.Fatalf("rendered covers = %#v, want bare scalar NO-SPEC", fields["covers"])
	}

	roundTripped, err := Parse(rendered)
	if err != nil {
		t.Fatalf("Parse rendered document: %v", err)
	}
	want := CoverageList{{Spec: "NO-SPEC", Coverage: "full"}}
	if got := roundTripped.Frontmatter.CoverageEntries(); !reflect.DeepEqual(got, want) {
		t.Errorf("round-tripped CoverageEntries() = %#v, want %#v", got, want)
	}
}

// TestCoveredSectionsReadsRetiredSpelling pins 033 §3: `implements` still
// parses so a branch written before the rename merges, and CoveredSections
// reports it without the caller knowing which spelling was on disk.
func TestCoveredSectionsReadsRetiredSpelling(t *testing.T) {
	retired := "---\nimplements: docs/specs/011-delivery-lifecycle.md\n---\n\n## 1. X {#sec-1}\n"
	doc, err := Parse([]byte(retired))
	if err != nil {
		t.Fatalf("Parse retired spelling: %v", err)
	}
	want := []string{"docs/specs/011-delivery-lifecycle.md"}
	if got := []string(doc.Frontmatter.CoveredSections()); !reflect.DeepEqual(got, want) {
		t.Errorf("CoveredSections() = %v, want %v", got, want)
	}

	current := "---\ncovers: a.md\n---\n\n## 1. X {#sec-1}\n"
	doc, err = Parse([]byte(current))
	if err != nil {
		t.Fatalf("Parse covers: %v", err)
	}
	if got := []string(doc.Frontmatter.CoveredSections()); !reflect.DeepEqual(got, []string{"a.md"}) {
		t.Errorf("CoveredSections() = %v, want [a.md]", got)
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
