package corpusindex

import "testing"

// TestContentHashStable asserts the same inputs always hash the same way.
func TestContentHashStable(t *testing.T) {
	a := ContentHash("doc", "title", "body")
	b := ContentHash("doc", "title", "body")
	if a != b {
		t.Errorf("ContentHash not stable: %q != %q", a, b)
	}
}

// TestContentHashIdenticalAcrossChunks models §7's freshness comparand: every
// chunk row of one subject stores the same hash, computed once over the
// subject's whole indexed text rather than per chunk.
func TestContentHashIdenticalAcrossChunks(t *testing.T) {
	task := struct{ Title, Body string }{"Fix the thing", "a long body that would split into more than one chunk"}
	subjectHash := ContentHash("task", task.Title, task.Body)
	// Every chunk of this subject is stamped with the same subjectHash by the
	// caller (internal/store) — corpusindex only guarantees the hash itself
	// is a pure function of the subject's text, not of chunk count.
	again := ContentHash("task", task.Title, task.Body)
	if subjectHash != again {
		t.Errorf("hash differs across calls for the same subject text")
	}
}

// TestContentHashChangesWithText asserts a changed input changes the hash —
// the whole freshness mechanism (§7) depends on this.
func TestContentHashChangesWithText(t *testing.T) {
	a := ContentHash("doc", "title", "body")
	b := ContentHash("doc", "title", "body, edited")
	if a == b {
		t.Error("ContentHash did not change when the text changed")
	}
}

// TestContentHashKindIsPartOfIdentity asserts two subjects of different
// kinds with coincidentally identical text still hash differently.
func TestContentHashKindIsPartOfIdentity(t *testing.T) {
	a := ContentHash("doc", "same text")
	b := ContentHash("task", "same text")
	if a == b {
		t.Error("ContentHash collided across kinds with identical text")
	}
}

// TestContentHashFieldBoundariesMatter asserts concatenating inputs
// differently produces a different hash, even when the joined bytes match.
func TestContentHashFieldBoundariesMatter(t *testing.T) {
	a := ContentHash("k", "ab", "c")
	b := ContentHash("k", "a", "bc")
	if a == b {
		t.Error("ContentHash collided across differently split inputs with the same joined bytes")
	}
}
