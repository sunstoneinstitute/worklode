package designdoc

import (
	"strings"
	"testing"
)

func TestEditingTitleRerendersHeading(t *testing.T) {
	src := "# Title\n\n## 1. Old {#sec-1}\n\nBody.\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Sections[0].Title = "New"
	want := "# Title\n\n## 1. New {#sec-1}\n\nBody.\n"
	if got := string(doc.Bytes()); got != want {
		t.Errorf("Bytes() = %q, want %q", got, want)
	}
}

func TestEditingBodyIsEmitted(t *testing.T) {
	src := "## 1. X {#sec-1}\n\nOld body.\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Sections[0].Body = "\nNew body.\n"
	want := "## 1. X {#sec-1}\n\nNew body.\n"
	if got := string(doc.Bytes()); got != want {
		t.Errorf("Bytes() = %q, want %q", got, want)
	}
}

// TestEditingOneSectionLeavesOthersVerbatim is the point of lazy re-render:
// touching §1 must not reformat §2's odd but legal spacing.
func TestEditingOneSectionLeavesOthersVerbatim(t *testing.T) {
	src := "## 1. One {#sec-1}\n\na\n\n##\t2.\tTwo   {#sec-2}   \n\nb\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Sections[0].Title = "Uno"
	got := string(doc.Bytes())
	if !strings.Contains(got, "## 1. Uno {#sec-1}\n") {
		t.Errorf("edited heading not re-rendered: %q", got)
	}
	if !strings.Contains(got, "##\t2.\tTwo   {#sec-2}   \n") {
		t.Errorf("untouched heading was reformatted: %q", got)
	}
}

func TestRenderHeadingShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		src, set, want string
	}{
		"top level keeps trailing dot": {
			src: "## 1. Old {#sec-1}\n", set: "New", want: "## 1. New {#sec-1}\n",
		},
		"subsection has no trailing dot": {
			src: "### 1.1 Old {#sec-1.1}\n", set: "New", want: "### 1.1 New {#sec-1.1}\n",
		},
		"letter suffix survives": {
			src: "### 2.1a Old {#sec-2.1a}\n", set: "New", want: "### 2.1a New {#sec-2.1a}\n",
		},
		"unnumbered heading": {
			src: "## Old\n", set: "New", want: "## New\n",
		},
		"numbered without anchor": {
			src: "## 3. Old\n", set: "New", want: "## 3. New\n",
		},
		"crlf preserved": {
			src: "## 1. Old {#sec-1}\r\n", set: "New", want: "## 1. New {#sec-1}\r\n",
		},
		"no trailing newline": {
			src: "## 1. Old {#sec-1}", set: "New", want: "## 1. New {#sec-1}",
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			doc.Sections[0].Title = tc.set
			if got := string(doc.Bytes()); got != tc.want {
				t.Errorf("Bytes() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEditingAnchorAndNumber(t *testing.T) {
	src := "## 1. X {#sec-1}\n\nb\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Sections[0].Number = "4"
	doc.Sections[0].Anchor = "sec-4"
	want := "## 4. X {#sec-4}\n\nb\n"
	if got := string(doc.Bytes()); got != want {
		t.Errorf("Bytes() = %q, want %q", got, want)
	}
}

func TestEditingFrontmatterRerenders(t *testing.T) {
	src := "---\nstatus: draft\nissued: 2026-08-02\n---\n\n## 1. X {#sec-1}\n\nb\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Frontmatter.Status = "accepted"
	got := string(doc.Bytes())
	if !strings.Contains(got, "status: accepted") {
		t.Errorf("status not re-rendered: %q", got)
	}
	if !strings.Contains(got, "issued: \"2026-08-02\"") && !strings.Contains(got, "issued: 2026-08-02") {
		t.Errorf("issued lost: %q", got)
	}
	if !strings.HasSuffix(got, "## 1. X {#sec-1}\n\nb\n") {
		t.Errorf("body damaged: %q", got)
	}
}

// TestUneditedFrontmatterStaysVerbatim protects comments and key order in the
// 26 existing headers, which a round-trip through the YAML marshaller loses.
func TestUneditedFrontmatterStaysVerbatim(t *testing.T) {
	src := "---\n# a comment\nstatus: draft\nrequires:\n  - a.md\n---\n\n## 1. X {#sec-1}\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := string(doc.Bytes()); got != src {
		t.Errorf("Bytes() = %q, want %q", got, src)
	}
}
