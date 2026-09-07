package store

import (
	"errors"
	"fmt"
	"testing"
)

// cycleBody is a one-section spec whose frontmatter carries exactly the
// amends/replaces block the caller passes (or none, for an empty string).
func cycleBody(anchorMaps string) string {
	return "---\nstatus: draft\n" + anchorMaps + "---\n\n# Doc\n\n## 1. One {#sec-1}\n\nx\n"
}

// seedCycleDoc creates spec `number` in p1, slugged NNN-<letter>, with the
// given frontmatter block.
func seedCycleDoc(t *testing.T, s *Store, number int, letter, anchorMaps string) int64 {
	t.Helper()
	d := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: number,
		Slug: fmt.Sprintf("%03d-%s", number, letter), Body: cycleBody(anchorMaps),
		CreatedBy: "stig",
	})
	return d.ID
}

// TestDocAmendsCycleDirectRejected: two sections amending each other close a
// loop, so the write that closes it is refused (026 §4.1).
func TestDocAmendsCycleDirectRejected(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	a := seedCycleDoc(t, s, 41, "a", "")
	seedCycleDoc(t, s, 42, "b", "amends:\n  \"#sec-1\": 041-a.md#sec-1\n")

	_, err := updateDocBody(t, s, a, cycleBody("amends:\n  \"#sec-1\": 042-b.md#sec-1\n"))
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("update closing the loop: err = %v, want ErrCycle", err)
	}
}

// TestDocAmendsCycleTransitiveRejected: the loop is three hops long and mixes
// the two relations that share the graph — `amends` and `replaces` both mean
// "this newer section acts on that older one".
func TestDocAmendsCycleTransitiveRejected(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	a := seedCycleDoc(t, s, 41, "a", "")
	seedCycleDoc(t, s, 42, "b", "amends:\n  \"#sec-1\": 041-a.md#sec-1\n")
	seedCycleDoc(t, s, 43, "c", "replaces:\n  \"#sec-1\": 042-b.md#sec-1\n")

	_, err := updateDocBody(t, s, a, cycleBody("amends:\n  \"#sec-1\": 043-c.md#sec-1\n"))
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("update closing the 3-hop loop: err = %v, want ErrCycle", err)
	}
}

// TestDocAmendsChainAccepted: a chain is not a loop, and a document-scoped
// claim is outside the graph entirely — it is a banner reference `lode show
// --inline` never folds (026 §3.2), so two documents may amend each other at
// document level without any reading becoming unanswerable.
func TestDocAmendsChainAccepted(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	seedCycleDoc(t, s, 41, "a", "")
	seedCycleDoc(t, s, 42, "b", "amends:\n  \"#sec-1\": 041-a.md#sec-1\n")
	c := seedCycleDoc(t, s, 43, "c", "amends:\n  \"#sec-1\": 042-b.md#sec-1\n")
	if edges := docEdges(t, s, c); len(edges) != 1 || edges[0].ToAnchor != "sec-1" {
		t.Fatalf("edges of the chain's head = %+v, want one amends edge onto sec-1", edges)
	}

	d := seedCycleDoc(t, s, 44, "d", "")
	seedCycleDoc(t, s, 45, "e", "amends:\n  \".\": 044-d.md\n")
	if _, err := updateDocBody(t, s, d, cycleBody("amends:\n  \".\": 045-e.md\n")); err != nil {
		t.Fatalf("document-level mutual amends: %v", err)
	}
}
