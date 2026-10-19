package mcp

import "testing"

func TestSimilarity(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"", "", 1},
		{"bug", "bug", 1},
		{"abc", "xyz", 0},
		{"bgu", "bug", 1.0 / 3},
		{"kitten", "sitting", 1 - 3.0/7},
	} {
		if got := similarity(c.a, c.b); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("similarity(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
