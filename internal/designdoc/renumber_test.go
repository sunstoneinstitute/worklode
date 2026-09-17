package designdoc

import (
	"strings"
	"testing"
)

// renumbered parses src, renumbers it, and returns the rendered result.
func renumbered(t *testing.T, src string) string {
	t.Helper()
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Renumber(d, DepthLimit); err != nil {
		t.Fatalf("Renumber: %v", err)
	}
	return string(d.Bytes())
}

// TestRenumberFixesInsertedSection is the case the flag exists for: a section
// pasted into the middle leaves every number below it one behind, and its own
// anchor copied from wherever it came from.
func TestRenumberFixesInsertedSection(t *testing.T) {
	got := renumbered(t, `---
status: draft
---

# Title

## 1. First {#sec-1}

Body.

## 1. Pasted in {#sec-9}

Body.

## 2. Third {#sec-2}

### 1.1 Under third {#sec-1.1}

Body.
`)
	want := `---
status: draft
---

# Title

## 1. First {#sec-1}

Body.

## 2. Pasted in {#sec-2}

Body.

## 3. Third {#sec-3}

### 3.1 Under third {#sec-3.1}

Body.
`
	if got != want {
		t.Errorf("renumbered:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenumberKeepsZeroOpening: an orientation section numbered 0 sets the
// sequence's start, so the body still opens at 1. Only the top level may.
func TestRenumberKeepsZeroOpening(t *testing.T) {
	got := renumbered(t, `# Title

## 0. Decision {#sec-0}

## 5. Options {#sec-5}

### 5.9 One option {#sec-5.9}
`)
	want := `# Title

## 0. Decision {#sec-0}

## 1. Options {#sec-1}

### 1.1 One option {#sec-1.1}
`
	if got != want {
		t.Errorf("renumbered:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenumberLeavesUnnumberedHeadings: numbers are normalised, never
// introduced. An unnumbered heading keeps its shape, and the sequence beneath
// it restarts, so a document with an unnumbered appendix is not rewritten into
// one without.
func TestRenumberLeavesUnnumberedHeadings(t *testing.T) {
	got := renumbered(t, `# Title

## Overview

## 3. Body {#sec-3}

## Appendix

### Notes
`)
	want := `# Title

## Overview

## 1. Body {#sec-1}

## Appendix

### Notes
`
	if got != want {
		t.Errorf("renumbered:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenumberKeepsInsertSuffix: a letter-suffixed insert (025 §3) is what a
// section added after acceptance looks like. It keeps its number and takes no
// slot, so the section after it is unaffected by its presence.
func TestRenumberKeepsInsertSuffix(t *testing.T) {
	got := renumbered(t, `# Title

## 1. One {#sec-1}

### 1.1 First {#sec-1.1}

### 1.1a Inserted later {#sec-1.1a}

### 1.2 Second {#sec-1.2}
`)
	if !strings.Contains(got, "### 1.1a Inserted later {#sec-1.1a}") {
		t.Errorf("the insert was renumbered:\n%s", got)
	}
	if !strings.Contains(got, "### 1.2 Second {#sec-1.2}") {
		t.Errorf("the insert consumed a counter slot:\n%s", got)
	}
}

// TestRenumberDeeperThanLimitUntouched: a heading below the addressability
// limit is legal content with no number (025 §6), so it is left alone rather
// than given one.
func TestRenumberDeeperThanLimitUntouched(t *testing.T) {
	got := renumbered(t, `# Title

## 1. One {#sec-1}

### 1.1 Two {#sec-1.1}

#### 1.1.1 Three {#sec-1.1.1}

##### Four
`)
	if !strings.Contains(got, "##### Four\n") {
		t.Errorf("a below-limit heading was rewritten:\n%s", got)
	}
}

// TestRenumberRefusesUnderivable: two shapes have no answer to derive, and
// both are reported at once rather than guessed at.
func TestRenumberRefusesUnderivable(t *testing.T) {
	t.Run("numbered under unnumbered", func(t *testing.T) {
		d, err := Parse([]byte("# T\n\n## Unnumbered\n\n### 4.1 Child {#sec-4.1}\n"))
		if err != nil {
			t.Fatal(err)
		}
		err = Renumber(d, DepthLimit)
		if err == nil || !strings.Contains(err.Error(), "parent is not") {
			t.Fatalf("err = %v, want it to name the unnumbered parent", err)
		}
	})
	t.Run("insert whose parent moved", func(t *testing.T) {
		d, err := Parse([]byte("# T\n\n## 5. One {#sec-5}\n\n### 5.1a Insert {#sec-5.1a}\n"))
		if err != nil {
			t.Fatal(err)
		}
		err = Renumber(d, DepthLimit)
		if err == nil || !strings.Contains(err.Error(), "defeat the suffix") {
			t.Fatalf("err = %v, want it to say the insert's parent moved", err)
		}
	})
}

// TestRenumberIsIdempotent: a document already in order comes back byte for
// byte, so running the flag on a clean body is a no-op rather than a diff.
func TestRenumberIsIdempotent(t *testing.T) {
	src := `---
status: draft
---

# Title

## 1. One {#sec-1}

Body with a {#sec-2} looking thing in it.

### 1.1 Under one {#sec-1.1}

## 2. Two {#sec-2}
`
	if got := renumbered(t, src); got != src {
		t.Errorf("renumbering a clean document changed it:\n%s", got)
	}
	if got := renumbered(t, renumbered(t, src)); got != src {
		t.Errorf("second pass differs:\n%s", got)
	}
}
