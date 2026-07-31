package skillhash

import "testing"

// TestSumPinnedFixture pins a known file set to its pre-refactor hash value
// (computed against skillsync's original contentHash before it moved here).
// A changed value here means every skill in the org would re-sync and
// re-embed on next deploy.
func TestSumPinnedFixture(t *testing.T) {
	files := []File{
		{Path: "SKILL.md", Data: []byte("---\nname: demo\n---\nbody")},
		{Path: "scripts/check.sh", Data: []byte("#!/bin/sh\necho ok\n"), Exec: true},
		{Path: "references/notes.md", Data: []byte("nested notes")},
	}
	const want = "da939cbd2bfa555365dfd016d7adbab2f754615d8b2ed6f19bac2732f10ff8e6"
	if got := Sum(files); got != want {
		t.Fatalf("Sum() = %s, want %s (pinned fixture value changed)", got, want)
	}
}

func TestSumOrderIndependent(t *testing.T) {
	a := []File{{Path: "a", Data: []byte("xy")}, {Path: "b", Data: []byte("")}}
	b := []File{{Path: "b", Data: []byte("")}, {Path: "a", Data: []byte("xy")}}
	if Sum(a) != Sum(b) {
		t.Fatal("Sum should not depend on input order")
	}
}

// The length prefix keeps a concatenation collision from hashing equal.
func TestSumNoConcatCollision(t *testing.T) {
	a := Sum([]File{{Path: "a", Data: []byte("xy")}, {Path: "b", Data: []byte("")}})
	b := Sum([]File{{Path: "a", Data: []byte("x")}, {Path: "b", Data: []byte("y")}})
	if a == b {
		t.Fatalf("collision: %s", a)
	}
}

func TestSumExecBitChangesHash(t *testing.T) {
	off := Sum([]File{{Path: "s.sh", Data: []byte("x")}})
	on := Sum([]File{{Path: "s.sh", Data: []byte("x"), Exec: true}})
	if off == on {
		t.Fatal("chmod +x did not change the content hash")
	}
}
