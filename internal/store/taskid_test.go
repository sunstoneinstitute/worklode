package store

import (
	"slices"
	"testing"
)

func TestCompareTaskIDs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int // sign of the expected result
	}{
		// numeric, not lexical: WL-10 sorts after WL-9.
		{"WL-9", "WL-10", -1},
		{"WL-10", "WL-9", 1},
		{"WL-2", "WL-10", -1},
		// equal ids compare 0.
		{"WL-3", "WL-3", 0},
		// key is compared lexically first.
		{"SW-1", "WL-1", -1},
		// key precedence beats the number: SW-100 still sorts before WL-1.
		{"SW-100", "WL-1", -1},
		// multi-digit keys of different length compare lexically by key.
		{"DEMO-1", "WL-1", -1},
	}
	for _, c := range cases {
		got := CompareTaskIDs(c.a, c.b)
		if sign(got) != c.want {
			t.Errorf("CompareTaskIDs(%q, %q) = %d, want sign %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSortTaskIDs(t *testing.T) {
	t.Parallel()
	ids := []string{"WL-10", "SW-2", "WL-9", "SW-10", "WL-1", "SW-1"}
	SortTaskIDs(ids)
	want := []string{"SW-1", "SW-2", "SW-10", "WL-1", "WL-9", "WL-10"}
	if !slices.Equal(ids, want) {
		t.Errorf("SortTaskIDs = %v, want %v", ids, want)
	}
}

func TestCompareTaskIDsMalformed(t *testing.T) {
	t.Parallel()
	// Well-formed ids sort before malformed ones.
	if CompareTaskIDs("WL-1", "bogus") >= 0 {
		t.Errorf("well-formed WL-1 should sort before malformed 'bogus'")
	}
	if CompareTaskIDs("bogus", "WL-1") <= 0 {
		t.Errorf("malformed 'bogus' should sort after well-formed WL-1")
	}
	// A non-numeric suffix is malformed.
	if CompareTaskIDs("WL-x", "WL-1") <= 0 {
		t.Errorf("WL-x (non-numeric suffix) is malformed, sorts after WL-1")
	}
	// Two malformed ids compare lexically without panicking.
	if CompareTaskIDs("abc", "abd") >= 0 {
		t.Errorf("malformed ids compared lexically: abc < abd")
	}
	if CompareTaskIDs("dup", "dup") != 0 {
		t.Errorf("identical malformed ids compare 0")
	}
}

// sign returns -1, 0, or +1 matching the sign of n.
func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}
