package designdoc

import (
	"slices"
	"testing"
)

// TestDeriveNumbersReadingOrder is WL-REQ-165: numbers come from position and
// depth alone, a letter-suffixed insert consumes a counter, and a slugged
// entry is unnumbered and takes no counter slot.
func TestDeriveNumbersReadingOrder(t *testing.T) {
	got := DeriveNumbers([]Entry{
		{Depth: 2}, {Depth: 3}, {Depth: 3}, {Depth: 2}, {Depth: 2},
		{Depth: 2, Slug: "sec-open-questions"}, {Depth: 3}, {Depth: 2},
	})
	wantNumbers := []string{"1", "1.1", "1.2", "2", "3", "", "", "4"}
	wantAnchors := []string{"sec-1", "sec-1.1", "sec-1.2", "sec-2", "sec-3", "sec-open-questions", "sec-open-questions.1", "sec-4"}
	var numbers, anchors []string
	for _, d := range got {
		numbers, anchors = append(numbers, d.Number), append(anchors, d.Anchor)
	}
	if !slices.Equal(numbers, wantNumbers) || !slices.Equal(anchors, wantAnchors) {
		t.Fatalf("numbers %v anchors %v, want %v %v", numbers, anchors, wantNumbers, wantAnchors)
	}
}

// TestDeriveAnchorsRenumbersInsert: a spec with sec-3a at the top level
// renders it as 4.
func TestDeriveAnchorsRenumbersInsert(t *testing.T) {
	d, err := Parse([]byte("# T\n\n## 3. C {#sec-3}\n\nc\n\n## 3a D {#sec-3a}\n\nd\n\n## Open {#sec-open}\n\no\n"))
	if err != nil {
		t.Fatal(err)
	}
	d.DeriveAnchors()
	got := string(d.Bytes())
	want := "# T\n\n## 1. C {#sec-1}\n\nc\n\n## 2. D {#sec-2}\n\nd\n\n## Open {#sec-open}\n\no\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestHeadingRuleRef: the rule ref the renderer prints between heading text
// and anchor (WL-REQ-1299) is not part of the title, and the stored form drops it.
func TestHeadingRuleRef(t *testing.T) {
	src := "## 4. Sections and anchors (WL-REQ-165) {#sec-4}\n\nx\n"
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	s := d.Sections[0]
	if s.Title != "Sections and anchors" || s.Ref != "WL-REQ-165" || s.Number != "4" || s.Anchor != "sec-4" {
		t.Fatalf("parsed %+v", s)
	}
	if string(d.Bytes()) != src {
		t.Fatalf("round trip changed the text: %q", d.Bytes())
	}
	if got := StripRuleRefs(src); got != "## 4. Sections and anchors {#sec-4}\n\nx\n" {
		t.Fatalf("StripRuleRefs = %q", got)
	}
	if got := Editable(src, map[string]string{"sec-4": "WL-REQ-165"}); got != "## 4. Sections and anchors {#sec-4 rule=WL-REQ-165}\n\nx\n" {
		t.Fatalf("Editable = %q", got)
	}
	n := NewSection(2, "1", "New", "sec-1", "")
	n.Ref = "WL-RULE-7"
	if got := n.Heading(); got != "## 1. New (WL-RULE-7) {#sec-1}\n" {
		t.Fatalf("rendered %q", got)
	}
}
