package review

import (
	"strings"
	"testing"
)

func TestRelocate(t *testing.T) {
	old := "a\nb\ntarget := 1\nc\n"
	q := "target := 1"
	s, e := strings.Index(old, q), strings.Index(old, q)+len(q)
	cases := []struct {
		name      string
		newText   string
		wantQuote bool
		wantStage string
	}{
		{"unchanged", old, true, "exact"},
		{"two lines inserted above", "x\ny\n" + old, true, "lcs"},
		{"line reindented", "a\nb\n    target := 1\nc\n", true, "fuzzy"},
		{"moved far away", "a\nb\nc\n" + strings.Repeat("z\n", 40) + "target := 1\n", true, "scan"},
		{"deleted", "a\nb\nc\n", false, "drifted"},
		{"multibyte text before the quote", "æøå\n" + old, true, "lcs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ns, ne, stage, ok := Relocate(old, c.newText, s, e, q)
			if ok != c.wantQuote || stage != c.wantStage {
				t.Fatalf("got ok=%v stage=%q, want ok=%v stage=%q", ok, stage, c.wantQuote, c.wantStage)
			}
			if ok && c.newText[ns:ne] != q {
				t.Fatalf("new[%d:%d] = %q, want %q", ns, ne, c.newText[ns:ne], q)
			}
		})
	}
}

func TestRelocateRewrappedLine(t *testing.T) {
	old := "one two\nthree four\n"
	q := "two\nthree"
	s := strings.Index(old, q)
	newText := "one two three four\n"
	ns, ne, stage, ok := Relocate(old, newText, s, s+len(q), q)
	if !ok || stage != "exact" || newText[ns:ne] != "two three" {
		t.Fatalf("got ok=%v stage=%q range=%q", ok, stage, newText[ns:ne])
	}
}

func TestRelocateShortQuoteNeverFuzzy(t *testing.T) {
	old := "a\nb\nx := 1\nc\n"
	q := "x := 1" // 6 bytes, under the fuzzy guard
	s := strings.Index(old, q)
	newText := "a\nb\n    x := 1\nc\n"
	ns, ne, stage, ok := Relocate(old, newText, s, s+len(q), q)
	if !ok || stage != "scan" || newText[ns:ne] != q {
		t.Fatalf("got ok=%v stage=%q range=%q", ok, stage, newText[ns:ne])
	}
}

func TestRelocateScanNearestPredicted(t *testing.T) {
	old := "p\nq\nthe needle here\nr\n"
	q := "needle"
	s := strings.Index(old, q)
	// A decoy hit comes first; the hit on the line after q is nearest the
	// predicted offset.
	newText := "needle decoy\np\nq\nchanged needle\nr\n"
	want := strings.LastIndex(newText, q)
	ns, ne, stage, ok := Relocate(old, newText, s, s+len(q), q)
	if !ok || stage != "scan" || ns != want || ne != want+len(q) {
		t.Fatalf("got ok=%v stage=%q range=[%d,%d), want [%d,%d)", ok, stage, ns, ne, want, want+len(q))
	}
}

func lines(s ...string) []string { return s }

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestReanchorLine(t *testing.T) {
	old := lines("a", "b", "target := 1", "c")
	cases := []struct {
		name      string
		newFile   []string
		wantLine  int
		wantStage string
		wantOK    bool
	}{
		{"unchanged", old, 3, "exact", true},
		{"two lines inserted above", lines("x", "y", "a", "b", "target := 1", "c"), 5, "lcs", true},
		{"line reindented", lines("a", "b", "    target := 1", "c"), 3, "fuzzy", true},
		{"moved far away", append(lines("a", "b", "c"), append(repeat("z", 40), "target := 1")...), 44, "scan", true},
		{"deleted", lines("a", "b", "c"), 0, "drifted", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line, stage, ok := ReanchorLine(old, c.newFile, 3, "target := 1")
			if line != c.wantLine || stage != c.wantStage || ok != c.wantOK {
				t.Errorf("got (%d, %q, %v), want (%d, %q, %v)", line, stage, ok, c.wantLine, c.wantStage, c.wantOK)
			}
		})
	}
}
