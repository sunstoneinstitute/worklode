package implements_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/graphproj/graphtest"
	"github.com/sunstoneinstitute/worklode/internal/kg/implements"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
)

// TestPinAnnotationRoundTrip proves the RDF 1.2 annotation encoding survives
// a real store: the document PUTs as written, and the pin reads back through
// the annotation syntax the stale-claim query of 025 §11.5 uses, keyed on
// the edge rather than on the reifier's own IRI.
func TestPinAnnotationRoundTrip(t *testing.T) {
	base := graphtest.Endpoint(t)

	// The endpoint may be shared, so the graph is run-unique and dropped on
	// cleanup (graphtest.PutGraph).
	graph := iri.GraphNS + fmt.Sprintf("test/observed/repo-implements-%d", time.Now().UnixNano())
	component := iri.Component("github.com/sunstoneinstitute/worklode")
	section := iri.Section("spec-worklode-025", "sec-11.5")
	pinned := iri.DocVersion("spec-worklode-025", 3)

	doc := graphproj.Document(implements.Triples([]implements.Claim{
		{Component: component, Section: section, Pinned: pinned},
	}))
	graphtest.PutGraph(t, base, graph, doc)

	rows := graphtest.Select(t, base, fmt.Sprintf(
		"SELECT ?c ?s ?v WHERE { GRAPH <%s> { <<?c <%s> ?s>> <%s> ?v } }",
		graph, iri.Term("implements"), iri.Term("pinnedVersion")))

	if len(rows) != 1 {
		t.Fatalf("annotation query returned %d rows, want 1: %v\ndocument:\n%s", len(rows), rows, doc)
	}
	got := rows[0]
	if got["c"] != component || got["s"] != section || got["v"] != pinned {
		t.Errorf("got %v; want component %s, section %s, pin %s", got, component, section, pinned)
	}
}
