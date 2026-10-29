package mdfence

import "testing"

func TestTracker(t *testing.T) {
	lines := []string{"```` ```x ```` text", "a", "````", "```", "b", "````", "~~~ go", "c", "~~~", "d"}
	want := []bool{false, false, true, true, true, true, true, true, true, false}
	var tr Tracker
	for i, l := range lines {
		if got := tr.Fence(l); got != want[i] {
			t.Errorf("line %d %q: Fence = %v, want %v", i, l, got, want[i])
		}
	}
}
