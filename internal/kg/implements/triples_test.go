package implements_test

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/implements"
)

func TestTriples(t *testing.T) {
	claims := []implements.Claim{
		{
			Component: "https://worklode.io/ns/id/component/github.com/sunstoneinstitute/worklode",
			Section:   "https://worklode.io/ns/id/section/spec-worklode-004/sec-4",
			Pinned:    "https://worklode.io/ns/id/doc/spec-worklode-004/v2",
		},
		{
			Component: "https://worklode.io/ns/id/component/github.com/sunstoneinstitute/worklode",
			Section:   "https://worklode.io/ns/id/section/spec-worklode-013/sec-3.1",
			Pinned:    "https://worklode.io/ns/id/doc/spec-worklode-013/v1",
		},
	}
	got := string(graphproj.Document(implements.Triples(claims)))
	want := "<https://worklode.io/ns/id/component/github.com/sunstoneinstitute/worklode> " +
		"<https://worklode.io/ns/ontology#implements> " +
		"<https://worklode.io/ns/id/section/spec-worklode-004/sec-4> .\n" +
		"<https://worklode.io/ns/id/component/github.com/sunstoneinstitute/worklode> " +
		"<https://worklode.io/ns/ontology#implements> " +
		"<https://worklode.io/ns/id/section/spec-worklode-013/sec-3.1> .\n"
	if got != want {
		t.Fatalf("Render = %q\nwant %q", got, want)
	}
}

// Two claims on one section differing only in pin (two entries pre-dedupe,
// or historic data) still emit one edge: the pin is not part of the edge.
func TestTriplesEdgeIsPinFree(t *testing.T) {
	claims := []implements.Claim{
		{Component: "https://x/c", Section: "https://x/s", Pinned: "https://x/d/v1"},
		{Component: "https://x/c", Section: "https://x/s", Pinned: "https://x/d/v2"},
	}
	got := string(graphproj.Document(implements.Triples(claims)))
	if want := "<https://x/c> <https://worklode.io/ns/ontology#implements> <https://x/s> .\n"; got != want {
		t.Fatalf("Document = %q; want the single deduplicated edge %q", got, want)
	}
}
