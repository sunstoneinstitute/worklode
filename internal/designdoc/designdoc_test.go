package designdoc

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestRoundTripCorpus is the byte-for-byte guarantee, checked against every
// document the repo actually has. A parser that normalises anything — line
// endings, trailing whitespace, heading spacing — fails here.
func TestRoundTripCorpus(t *testing.T) {
	var files []string
	for _, dir := range []string{"../../docs/specs", "../../docs/plans"} {
		matches, err := filepath.Glob(filepath.Join(dir, "*.md"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatal("no documents found; corpus test would pass vacuously")
	}

	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			doc, err := Parse(src)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := doc.Bytes(); !bytes.Equal(got, src) {
				t.Errorf("round-trip differs (%d bytes in, %d out)", len(src), len(got))
			}
			// Without this the round-trip passes vacuously: a scanner that
			// finds no headings puts the whole file in Preamble and emits it
			// back unchanged. Every document in the corpus has sections.
			if len(doc.Sections) == 0 {
				t.Error("no sections parsed; round-trip proves nothing")
			}
		})
	}
}

// TestRoundTripPreservesQuirks covers what the corpus cannot: every file in
// docs/ is LF-only with a trailing newline, so a parser that normalises line
// endings or appends a final newline passes the corpus test regardless.
func TestRoundTripPreservesQuirks(t *testing.T) {
	for name, src := range map[string]string{
		"crlf":            "# Title\r\n\r\n## 1. First {#sec-1}\r\n\r\nBody.\r\n",
		"mixed endings":   "# Title\n\n## 1. First {#sec-1}\r\n\r\nBody.\n",
		"no trailing nl":  "# Title\n\n## 1. First {#sec-1}\n\nBody.",
		"tabs after hash": "# Title\n\n##\t1.\tFirst {#sec-1}\n\nBody.\n",
		"trailing space":  "# Title\n\n## 1. First {#sec-1}   \n\nBody.\n",
		"crlf no anchor":  "## Unnumbered\r\n\r\nBody.\r\n",
		"no headings":     "Just prose, no headings at all.\n",
		"empty":           "",
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := Parse([]byte(src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := string(doc.Bytes()); got != src {
				t.Errorf("round-trip:\n got %q\nwant %q", got, src)
			}
		})
	}
}

// TestHeadingsInsideFencesAreNotSections matters because specs quote shell
// and markdown, and a "# comment" line in a code block is not a section.
func TestHeadingsInsideFencesAreNotSections(t *testing.T) {
	src := "# Title\n\n" +
		"## 1. Real {#sec-1}\n\n" +
		"```sh\n## not a section\n```\n\n" +
		"~~~markdown\n### also not a section {#sec-fake}\n~~~\n\n" +
		"## 2. Also real {#sec-2}\n\nEnd.\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var anchors []string
	for _, s := range doc.Sections {
		anchors = append(anchors, s.Anchor)
	}
	if len(anchors) != 2 || anchors[0] != "sec-1" || anchors[1] != "sec-2" {
		t.Errorf("anchors = %v, want [sec-1 sec-2]", anchors)
	}
	if got := string(doc.Bytes()); got != src {
		t.Errorf("round-trip differs")
	}
}

func TestParseLinksParentsAndChildren(t *testing.T) {
	src := "# Title\n\n" +
		"## 1. One {#sec-1}\n\na\n\n" +
		"### 1.1 One-one {#sec-1.1}\n\nb\n\n" +
		"#### 1.1.1 Deep {#sec-1.1.1}\n\nc\n\n" +
		"### 1.2 One-two {#sec-1.2}\n\nd\n\n" +
		"## 2. Two {#sec-2}\n\ne\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Sections) != 5 {
		t.Fatalf("got %d sections, want 5", len(doc.Sections))
	}
	one, oneOne, deep, oneTwo, two := doc.Sections[0], doc.Sections[1], doc.Sections[2], doc.Sections[3], doc.Sections[4]

	for _, tc := range []struct {
		sec  *Section
		want *Section
	}{
		{one, nil}, {oneOne, one}, {deep, oneOne}, {oneTwo, one}, {two, nil},
	} {
		if tc.sec.Parent != tc.want {
			t.Errorf("%s: Parent = %v, want %v", tc.sec.Anchor, anchorOf(tc.sec.Parent), anchorOf(tc.want))
		}
	}
	if len(one.Children) != 2 || one.Children[0] != oneOne || one.Children[1] != oneTwo {
		t.Errorf("sec-1 children = %v, want [sec-1.1 sec-1.2]", anchorsOf(one.Children))
	}
	if len(oneOne.Children) != 1 || oneOne.Children[0] != deep {
		t.Errorf("sec-1.1 children = %v, want [sec-1.1.1]", anchorsOf(oneOne.Children))
	}
	if len(two.Children) != 0 {
		t.Errorf("sec-2 children = %v, want none", anchorsOf(two.Children))
	}
	for i, s := range doc.Sections {
		if s.Index != i {
			t.Errorf("Sections[%d].Index = %d", i, s.Index)
		}
	}
}

// TestParseLinksSkippedLevel covers a document that jumps ## straight to
// ####; the deeper heading still nests under the nearest shallower one.
func TestParseLinksSkippedLevel(t *testing.T) {
	src := "## 1. One {#sec-1}\n\na\n\n#### Deep {#sec-deep}\n\nb\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Sections[1].Parent != doc.Sections[0] {
		t.Errorf("Parent = %v, want sec-1", anchorOf(doc.Sections[1].Parent))
	}
}

func anchorOf(s *Section) string {
	if s == nil {
		return "<nil>"
	}
	return s.Anchor
}

func anchorsOf(secs []*Section) []string {
	out := make([]string, 0, len(secs))
	for _, s := range secs {
		out = append(out, s.Anchor)
	}
	return out
}

func TestParseExtractsSectionFields(t *testing.T) {
	src := "# Spec 999 — Title\n\n## 1. First {#sec-1}\n\nBody text.\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Sections) != 1 {
		t.Fatalf("got %d sections, want 1", len(doc.Sections))
	}
	s := doc.Sections[0]
	if s.Level != 2 || s.Number != "1" || s.Title != "First" || s.Anchor != "sec-1" {
		t.Errorf("got level=%d number=%q title=%q anchor=%q", s.Level, s.Number, s.Title, s.Anchor)
	}
	if want := "\nBody text.\n"; s.Body != want {
		t.Errorf("Body = %q, want %q", s.Body, want)
	}
	if want := "# Spec 999 — Title\n\n"; doc.Preamble != want {
		t.Errorf("Preamble = %q, want %q", doc.Preamble, want)
	}
}

func TestBytesReproducesInput(t *testing.T) {
	src := "# Spec 999 — Title\n\n## 1. First {#sec-1}\n\nBody text.\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := string(doc.Bytes()); got != src {
		t.Errorf("Bytes() = %q, want %q", got, src)
	}
}
