package cli

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// boardLike builds the `lode board` column set with one long-titled row.
func boardLike(title, holder string) *table {
	tbl := newTable(
		column{header: "ID"},
		column{header: "PRIORITY"},
		titleColumn("TITLE"),
		holderColumn("HOLDER"),
	)
	tbl.add("WL-123", "critical", title, holder)
	return tbl
}

func lines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func widest(ls []string) int {
	n := 0
	for _, l := range ls {
		n = max(n, utf8.RuneCountInString(l))
	}
	return n
}

func TestTableUnlimitedWidthKeepsOneLinePerRow(t *testing.T) {
	var b strings.Builder
	title := "Word-wrap the title column so board rows never wrap the terminal"
	boardLike(title, "agent-1 (until 2026-08-16T12:00:00+02:00)").render(&b, 0)
	got := lines(b.String())
	if len(got) != 2 {
		t.Fatalf("want header + 1 row, got %d lines:\n%s", len(got), b.String())
	}
	if !strings.Contains(got[1], title) {
		t.Fatalf("title was broken up at unlimited width:\n%s", b.String())
	}
}

func TestTableWrapsTitleWithinTerminalWidth(t *testing.T) {
	var b strings.Builder
	boardLike("Word-wrap the title column so board rows never wrap the terminal", "-").render(&b, 60)
	got := lines(b.String())
	if w := widest(got); w > 60 {
		t.Fatalf("line of %d columns exceeds the 60-column terminal:\n%s", w, b.String())
	}
	if len(got) < 3 {
		t.Fatalf("the title should have wrapped onto further lines:\n%s", b.String())
	}
	// Word wrapping, not truncation: every word survives, unsplit.
	joined := strings.Join(strings.Fields(strings.Join(got[1:], " ")), " ")
	for _, word := range strings.Fields("Word-wrap the title column so board rows never wrap the terminal") {
		if !strings.Contains(joined, word) {
			t.Fatalf("word %q was lost or split:\n%s", word, b.String())
		}
	}
	// Continuation lines carry only the title; the fixed columns stay blank.
	if strings.Contains(got[2], "WL-123") || strings.Contains(got[2], "critical") {
		t.Fatalf("continuation line repeated the fixed columns:\n%s", b.String())
	}
}

func TestTableCharWrapsHolderUnderPressure(t *testing.T) {
	var b strings.Builder
	holder := "agent-1 (until 2026-08-16T12:00:00+02:00)"
	boardLike("Word-wrap the title column so board rows never wrap", holder).render(&b, 64)
	got := lines(b.String())
	if w := widest(got); w > 64 {
		t.Fatalf("line of %d columns exceeds the 64-column terminal:\n%s", w, b.String())
	}
	// The holder is squeezed but never below 16 columns, so the actor id
	// stays whole on its first line.
	if !strings.Contains(got[1], "agent-1") {
		t.Fatalf("holder actor id did not fit on the first line:\n%s", b.String())
	}
	// Wrapped, not truncated: the tail of the holder appears on a later line.
	if !strings.Contains(b.String(), "+02:00)") {
		t.Fatalf("holder was truncated rather than wrapped:\n%s", b.String())
	}
}

func TestTableGivesUpBelowMinimums(t *testing.T) {
	// Narrower than the fixed columns plus both minimums: the table must
	// still render every cell rather than dropping or truncating anything.
	var b strings.Builder
	boardLike("A very long title indeed for such a narrow terminal", "agent-1").render(&b, 20)
	out := b.String()
	for _, want := range []string{"WL-123", "critical", "narrow terminal", "agent-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q at an impossible width:\n%s", want, out)
		}
	}
}

func TestWrapWordsAtSplitsOverlongWord(t *testing.T) {
	got := wrapWordsAt("ok supercalifragilistic end", 10)
	want := []string{"ok", "supercalif", "ragilistic", "end"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}
